/* nocap-lock: PAM authentication in a forked child. */
#ifndef NOCAP_LOCK_AUTH_H
#define NOCAP_LOCK_AUTH_H

#include <stddef.h>
#include <sys/types.h>

#define AUTH_SERVICE "nocap-lock"

/* Result bytes written by the child on the pipe. */
#define AUTH_OK 'y'
#define AUTH_FAIL 'n'

/* Fork a child that authenticates `user` with `password` (len bytes, not
 * NUL-terminated). Returns the read end of a pipe the child writes one
 * result byte to, or -1. *pid receives the child pid. The caller zeroes its
 * own password copy; the child zeroes its copy before exiting. */
int auth_start(const char *user, const char *password, size_t len, pid_t *pid);

/* Read the result from fd (closes it and reaps the child). Returns 1 when
 * authenticated, 0 on failure, -1 when the child died without an answer. */
int auth_finish(int fd, pid_t pid);

#endif
