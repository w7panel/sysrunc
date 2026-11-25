#define _GNU_SOURCE
#include <fcntl.h>
#include <linux/magic.h>
#include <linux/stat.h>
#include <stdio.h>
#include <string.h>
#include <sys/statfs.h>
#include <sys/syscall.h>
#include <unistd.h>

struct cpu_snapshot {
    unsigned count;
    unsigned long long total[4];
};

static int read_cpu_snapshot(int fd, struct cpu_snapshot *snapshot) {
    char buf[65536];
    size_t used = 0;
    if (lseek(fd, 0, SEEK_SET) < 0) return -1;
    while (used < sizeof(buf) - 1) {
        ssize_t n = read(fd, buf + used, sizeof(buf) - 1 - used);
        if (n < 0) return -1;
        if (n == 0) break;
        used += (size_t)n;
    }
    if (used == sizeof(buf) - 1) return -1;
    buf[used] = 0;
    memset(snapshot, 0, sizeof(*snapshot));
    char *save = NULL;
    for (char *line = strtok_r(buf, "\n", &save); line; line = strtok_r(NULL, "\n", &save)) {
        if (strncmp(line, "cpu ", 4) == 0) {
            if (sscanf(line + 4, "%llu %llu %llu %llu", &snapshot->total[0],
                       &snapshot->total[1], &snapshot->total[2], &snapshot->total[3]) != 4) return -1;
        } else if (strncmp(line, "cpu", 3) == 0 && line[3] >= '0' && line[3] <= '9') {
            snapshot->count++;
        }
    }
    return snapshot->count > 0 ? 0 : -1;
}

static int check_stat_views(int fast, int fallback) {
    struct cpu_snapshot a, b, later;
    if (read_cpu_snapshot(fast, &a) || read_cpu_snapshot(fallback, &b)) {
        fprintf(stderr, "/proc/stat: failed to parse CPU lines\n"); return 20;
    }
    if (a.count != b.count) {
        fprintf(stderr, "/proc/stat: worker/FUSE CPU count differs (%u/%u)\n", a.count, b.count);
        return 21;
    }
    sleep(1);
    if (read_cpu_snapshot(fast, &later) || later.count != a.count) {
        fprintf(stderr, "/proc/stat: worker CPU count changed\n"); return 22;
    }
    for (unsigned i = 0; i < 4; i++) {
        if (later.total[i] < a.total[i]) {
            fprintf(stderr, "/proc/stat: CPU field %u decreased (%llu/%llu)\n",
                    i, a.total[i], later.total[i]);
            return 23;
        }
    }
    printf("/proc/stat: %u CPUs, worker and FUSE counts agree, worker counters monotonic\n", a.count);
    return 0;
}

int main(void) {
    const char *paths[] = {
        "/proc/cpuinfo", "/proc/diskstats", "/proc/meminfo", "/proc/stat",
        "/proc/swaps", "/proc/uptime", "/proc/loadavg",
    };
    for (unsigned i = 0; i < sizeof(paths) / sizeof(paths[0]); i++) {
        struct statx st = {0};
        if (syscall(SYS_statx, AT_FDCWD, paths[i], 0, STATX_BASIC_STATS, &st) != 0) {
            perror("statx"); return 1;
        }
        if (st.stx_size != 4096 || (st.stx_mode & 0777) != 0444) {
            fprintf(stderr, "%s: unexpected statx size/mode %llu/%o\n", paths[i],
                (unsigned long long)st.stx_size, st.stx_mode & 0777);
            return 2;
        }
        int fd = openat(AT_FDCWD, paths[i], O_RDONLY | O_CLOEXEC);
        if (fd < 0) { perror("openat"); return 3; }
        char name[256], target[256], data[129] = {0};
        snprintf(name, sizeof(name), "/proc/self/fd/%d", fd);
        int n = readlink(name, target, sizeof(target) - 1);
        if (n < 0) { perror("readlink"); return 4; }
        target[n] = 0;
        if (strncmp(target, "/memfd:sysrunc-", strlen("/memfd:sysrunc-")) != 0) {
            fprintf(stderr, "%s: expected memfd, got %s\n", paths[i], target);
            return 5;
        }
        if ((fcntl(fd, F_GETFL) & O_ACCMODE) != O_RDONLY || write(fd, "x", 1) >= 0) {
            fprintf(stderr, "%s: injected fd is writable\n", paths[i]);
            return 15;
        }
        n = read(fd, data, sizeof(data) - 1);
        if (n < 0) { perror("read"); return 6; }
        int fallback = openat(AT_FDCWD, paths[i], O_RDONLY | O_NONBLOCK);
        if (fallback < 0) { perror("fallback openat"); return 7; }
        struct statfs fs = {0};
        if (fstatfs(fallback, &fs) != 0) { perror("fallback fstatfs"); return 8; }
        if (fs.f_type != FUSE_SUPER_MAGIC) {
            fprintf(stderr, "%s: expected FUSE fallback, got %lx\n", paths[i], (unsigned long)fs.f_type);
            return 9;
        }
        if (strcmp(paths[i], "/proc/stat") == 0) {
            int rc = check_stat_views(fd, fallback);
            if (rc) return rc;
        }
        if (strcmp(paths[i], "/proc/meminfo") == 0) {
            char fallback_data[sizeof(data)] = {0};
            int fallback_n = read(fallback, fallback_data, sizeof(fallback_data) - 1);
            unsigned long long worker_total = 0, fuse_total = 0;
            if (fallback_n <= 0 ||
                sscanf(data, "MemTotal: %llu kB", &worker_total) != 1 ||
                sscanf(fallback_data, "MemTotal: %llu kB", &fuse_total) != 1 ||
                worker_total != fuse_total) {
                fprintf(stderr, "%s: worker and FUSE MemTotal differ (%llu/%llu kB)\n",
                    paths[i], worker_total, fuse_total);
                return 18;
            }
        }
        if (strcmp(paths[i], "/proc/uptime") == 0) {
            char fallback_data[sizeof(data)] = {0};
            int fallback_n = read(fallback, fallback_data, sizeof(fallback_data) - 1);
            double worker_uptime = 0, fuse_uptime = 0;
            if (fallback_n <= 0 ||
                sscanf(data, "%lf", &worker_uptime) != 1 ||
                sscanf(fallback_data, "%lf", &fuse_uptime) != 1 ||
                fuse_uptime < worker_uptime - 1 || fuse_uptime > worker_uptime + 5) {
                fprintf(stderr, "%s: worker and FUSE uptime differ (%.2f/%.2f seconds)\n",
                    paths[i], worker_uptime, fuse_uptime);
                return 19;
            }
        }
        if (strcmp(paths[i], "/proc/swaps") == 0) {
            char fallback_data[sizeof(data)];
            int fallback_n = read(fallback, fallback_data, sizeof(fallback_data));
            if (fallback_n != n || memcmp(data, fallback_data, n) != 0) {
                fprintf(stderr, "%s: worker and FUSE output differ (%d/%d bytes)\n",
                    paths[i], n, fallback_n);
                return 16;
            }
            size_t offset = (size_t)n;
            for (;;) {
                int next_n = read(fd, data, sizeof(data));
                fallback_n = read(fallback, fallback_data, sizeof(fallback_data));
                if (next_n < 0 || fallback_n != next_n ||
                    memcmp(data, fallback_data, next_n) != 0) {
                    fprintf(stderr, "%s: worker and FUSE output differ later (%d/%d bytes)\n",
                        paths[i], next_n, fallback_n);
                    if (next_n > 0 && fallback_n == next_n) {
                        for (int j = 0; j < next_n; j++) {
                            if (data[j] != fallback_data[j]) {
                                fprintf(stderr, "first difference at offset %zu: worker=%02x FUSE=%02x\n",
                                    offset + (size_t)j, (unsigned char)data[j], (unsigned char)fallback_data[j]);
                                break;
                            }
                        }
                    }
                    return 17;
                }
                if (next_n == 0) break;
                offset += (size_t)next_n;
            }
        }
        printf("%s: memfd bytes=%d, statx size=%llu, fallback=FUSE\n", paths[i], n,
            (unsigned long long)st.stx_size);
        close(fallback);
        close(fd);
    }
    int proc = openat(AT_FDCWD, "/proc", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (proc < 0) { perror("open /proc"); return 10; }
    int relative = openat(proc, "meminfo", O_RDONLY | O_CLOEXEC);
    if (relative < 0) { perror("relative openat meminfo"); return 11; }
    char link[256], target[256];
    snprintf(link, sizeof(link), "/proc/self/fd/%d", relative);
    int n = readlink(link, target, sizeof(target) - 1);
    if (n < 0) { perror("relative readlink"); return 12; }
    target[n] = 0;
    if (strncmp(target, "/memfd:sysrunc-meminfo", strlen("/memfd:sysrunc-meminfo")) != 0) {
        fprintf(stderr, "relative meminfo: expected memfd, got %s\n", target);
        return 13;
    }
    struct statx st = {0};
    if (syscall(SYS_statx, proc, "meminfo", 0, STATX_BASIC_STATS, &st) != 0 || st.stx_size != 4096) {
        perror("relative statx meminfo"); return 14;
    }
    printf("relative /proc/meminfo: memfd and statx passed\n");
    close(relative);
    close(proc);
    return 0;
}
