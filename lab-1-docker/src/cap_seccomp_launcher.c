
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <unistd.h>
#include <sys/prctl.h>
#include <sys/capability.h>
#include <seccomp.h>

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "Usage: %s PROGRAM [ARGS...]\n",
                argv[0]);
        return 1;
    }

    // Сбрасываем лишние capabilities
    if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) < 0) {
        perror("no_new_privs");
        return 1;
    }

    if (prctl(PR_CAP_AMBIENT,
              PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0) < 0) {
        perror("ambient caps");
        return 1;
    }

    cap_t caps = cap_init();
    if (!caps) return 1;

    if (cap_set_proc(caps) < 0) {
        perror("cap_set_proc");
        cap_free(caps);
        return 1;
    }
    cap_free(caps);

    // Запрещаем системный вызов для получения PID родительского процесса
    scmp_filter_ctx ctx = seccomp_init(SCMP_ACT_ALLOW);
    if (!ctx) return 1;
    
    int rc = seccomp_rule_add(
        ctx,
        SCMP_ACT_ERRNO(EPERM),
        SCMP_SYS(getppid),
        0
    );

    if (rc < 0) {
        errno = -rc;
        perror("seccomp_rule_add");
        seccomp_release(ctx);
        return 1;
    }

    // Применяем seccomp
    rc = seccomp_load(ctx);
    if (rc < 0) {
        errno = -rc;
        perror("seccomp_load");
        seccomp_release(ctx);
        return 1;
    }
    seccomp_release(ctx);

    // Заменяем исполняемую программу на целевую
    execvp(argv[1], &argv[1]);

    perror("execvp");
    return 1;
}
