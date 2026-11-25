#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <unistd.h>

int main(void) {
    int fd = open("/proc/sys/kernel/random", O_RDONLY | O_DIRECTORY);
    if (fd < 0) {
        perror("open random directory");
        return 2;
    }

    char buffer[512];
    errno = 0;
    long small = syscall(SYS_getdents64, fd, buffer, 16UL);
    int small_errno = errno;
    if (lseek(fd, 0, SEEK_SET) < 0) {
        perror("rewind random directory");
        return 2;
    }

    // The kernel's third argument is unsigned int. A raw syscall can carry
    // high bits; they must not let a seccomp handler write past the low 32-bit
    // count used by the kernel.
    errno = 0;
    long high = syscall(SYS_getdents64, fd, buffer, 0x100000010UL);
    int high_errno = errno;
    printf("small=%ld errno=%d high=%ld errno=%d\n",
           small, small_errno, high, high_errno);
    close(fd);
    return small == -1 && small_errno == EINVAL &&
                   high == -1 && high_errno == EINVAL
               ? 0 : 1;
}
