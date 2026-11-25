#define _GNU_SOURCE
#include <dirent.h>
#include <fcntl.h>
#include <linux/magic.h>
#include <linux/stat.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/statfs.h>
#include <sys/syscall.h>
#include <unistd.h>

int main(void) {
    const char *dir = "/proc/sys/kernel/random";
    const char *file = "/proc/sys/kernel/random/uuid";
    struct statx st = {0};
    if (syscall(SYS_statx, AT_FDCWD, file, 0, STATX_BASIC_STATS, &st) < 0) { perror("statx uuid"); return 1; }
    printf("uuid statx size=%llu mode=%o\n", (unsigned long long)st.stx_size, st.stx_mode & 0777);
    if (st.stx_size != 1024 || (st.stx_mode & 0777) != 0444) return 2;
    int dfd = openat(AT_FDCWD, dir, O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (dfd < 0) { perror("openat dir"); return 3; }
    struct statfs fs = {0};
    if (fstatfs(dfd, &fs) < 0) { perror("fstatfs dir"); return 4; }
    printf("dir fstatfs type=%lx proc=%lx fuse=%lx\n", (unsigned long)fs.f_type, (unsigned long)PROC_SUPER_MAGIC, (unsigned long)FUSE_SUPER_MAGIC);
    /* A host proc directory fd would allow "../" to escape the virtual mount. */
    if (fs.f_type != FUSE_SUPER_MAGIC) return 5;
    char buf[8192];
    char first[64];
    int first_n = syscall(SYS_getdents64, dfd, first, sizeof(first));
    if (first_n <= 0) { perror("partial getdents64"); return 22; }
    off_t first_cookie = lseek(dfd, 0, SEEK_CUR);
    if (first_cookie <= 0) { perror("first cookie"); return 23; }
    int dupfd = dup(dfd);
    if (dupfd < 0) { perror("dup dir"); return 24; }
    int remaining = syscall(SYS_getdents64, dupfd, buf, sizeof(buf));
    if (remaining <= 0 || lseek(dfd, 0, SEEK_CUR) <= first_cookie) {
        perror("shared directory cursor"); return 25;
    }
    close(dupfd);
    if (lseek(dfd, 0, SEEK_SET) != 0) { perror("rewind dir"); return 26; }
    int n = syscall(SYS_getdents64, dfd, buf, sizeof(buf));
    if (n < 0) { perror("getdents64"); return 6; }
    int found = 0;
    for (int off = 0; off < n;) {
        struct linux_dirent64 { unsigned long long ino; long long off; unsigned short reclen; unsigned char type; char name[]; } *e = (void *)(buf + off);
        if (strcmp(e->name, "uuid") == 0) found = 1;
        off += e->reclen;
    }
    printf("getdents64 bytes=%d uuid=%d\n", n, found);
    if (!found) return 7;
    if (syscall(SYS_getdents64, dfd, buf, sizeof(buf)) != 0) return 27;
    if (lseek(dfd, 0, SEEK_SET) != 0 || syscall(SYS_getdents64, dfd, buf, sizeof(buf)) != n) return 28;
    printf("getdents64 partial=%d shared-cookie=%lld rewind=passed\n", first_n, (long long)first_cookie);
    int parent_fd = openat(dfd, "../hostname", O_RDONLY | O_CLOEXEC);
    if (parent_fd < 0) { perror("openat parent hostname"); return 19; }
    if (fstatfs(parent_fd, &fs) < 0) { perror("fstatfs parent hostname"); return 20; }
    printf("parent hostname fstatfs type=%lx\n", (unsigned long)fs.f_type);
    if (fs.f_type != FUSE_SUPER_MAGIC) return 21;
    close(parent_fd);
    if (syscall(SYS_statx, dfd, "uuid", 0, STATX_BASIC_STATS, &st) < 0) { perror("statx relative uuid"); return 8; }
    printf("relative statx size=%llu\n", (unsigned long long)st.stx_size);
    if (st.stx_size != 1024) return 9;
    int fd = openat(dfd, "uuid", O_RDONLY | O_CLOEXEC);
    if (fd < 0) { perror("openat relative uuid"); return 10; }
    char link[256], target[256];
    snprintf(link, sizeof(link), "/proc/self/fd/%d", fd);
    n = readlink(link, target, sizeof(target) - 1);
    if (n < 0) { perror("readlink uuid"); return 11; }
    target[n] = 0;
    printf("relative uuid fd=%s\n", target);
    if (strncmp(target, "/memfd:sysrunc-proc-uuid", strlen("/memfd:sysrunc-proc-uuid")) != 0) return 12;
    if ((fcntl(fd, F_GETFL) & O_ACCMODE) != O_RDONLY || write(fd, "x", 1) >= 0) return 18;
    n = read(fd, buf, sizeof(buf));
    if (n < 36) { perror("read uuid"); return 13; }
    printf("relative uuid read bytes=%d\n", n);
    int fallback = openat(AT_FDCWD, "/proc/sys", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (fallback < 0) { perror("openat fallback dir"); return 14; }
    if (fstatfs(fallback, &fs) < 0) { perror("fstatfs fallback dir"); return 15; }
    printf("fallback fstatfs type=%lx\n", (unsigned long)fs.f_type);
    if (fs.f_type != FUSE_SUPER_MAGIC) return 16;
    if (syscall(SYS_getdents64, fallback, buf, sizeof(buf)) <= 0) return 29;
    if (syscall(SYS_statx, AT_FDCWD, "/proc/sys/net/ipv4/ip_unprivileged_port_start", 0, STATX_BASIC_STATS, &st) < 0) { perror("statx fallback file"); return 17; }
    printf("fallback statx mode=%o\n", st.stx_mode & 0777);
    close(fallback);
    close(fd); close(dfd);
    return 0;
}
