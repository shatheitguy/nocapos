/* nocap-lock: PAM authentication in a forked child so the UI never blocks. */
#include "auth.h"

#include <errno.h>
#include <fcntl.h>
#include <security/pam_appl.h>
#include <signal.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

struct conv_data {
	const char *user;
	char *password;
};

static int conv_fn(int n, const struct pam_message **msg,
		   struct pam_response **resp, void *appdata)
{
	struct conv_data *d = appdata;

	if (n <= 0 || n > PAM_MAX_NUM_MSG)
		return PAM_CONV_ERR;
	struct pam_response *r = calloc((size_t)n, sizeof(*r));
	if (!r)
		return PAM_BUF_ERR;

	for (int i = 0; i < n; i++) {
		switch (msg[i]->msg_style) {
		case PAM_PROMPT_ECHO_OFF:
			r[i].resp = strdup(d->password);
			break;
		case PAM_PROMPT_ECHO_ON:
			r[i].resp = strdup(d->user);
			break;
		case PAM_ERROR_MSG:
		case PAM_TEXT_INFO:
			r[i].resp = NULL;
			continue;
		default:
			goto fail;
		}
		if (!r[i].resp)
			goto fail;
	}
	*resp = r;
	return PAM_SUCCESS;

fail:
	for (int i = 0; i < n; i++) {
		if (r[i].resp) {
			explicit_bzero(r[i].resp, strlen(r[i].resp));
			free(r[i].resp);
		}
	}
	free(r);
	return PAM_CONV_ERR;
}

static int authenticate(const char *user, char *password)
{
	struct conv_data d = { .user = user, .password = password };
	struct pam_conv conv = { .conv = conv_fn, .appdata_ptr = &d };
	pam_handle_t *h = NULL;

	if (pam_start(AUTH_SERVICE, user, &conv, &h) != PAM_SUCCESS)
		return 0;
	int rc = pam_authenticate(h, 0);
	if (rc == PAM_SUCCESS)
		(void)pam_setcred(h, PAM_REFRESH_CRED);
	pam_end(h, rc);
	return rc == PAM_SUCCESS;
}

int auth_start(const char *user, const char *password, size_t len, pid_t *pid)
{
	int p[2];

	if (pipe2(p, O_CLOEXEC) < 0)
		return -1;

	pid_t child = fork();
	if (child < 0) {
		close(p[0]);
		close(p[1]);
		return -1;
	}
	if (child == 0) {
		close(p[0]);
		signal(SIGPIPE, SIG_IGN);
		char buf[513];
		if (len >= sizeof(buf))
			len = sizeof(buf) - 1;
		memcpy(buf, password, len);
		buf[len] = '\0';
		int ok = authenticate(user, buf);
		explicit_bzero(buf, sizeof(buf));
		char c = ok ? AUTH_OK : AUTH_FAIL;
		ssize_t w;
		do {
			w = write(p[1], &c, 1);
		} while (w < 0 && errno == EINTR);
		_exit(0);
	}

	close(p[1]);
	*pid = child;
	return p[0];
}

int auth_finish(int fd, pid_t pid)
{
	char c = 0;
	ssize_t r;

	do {
		r = read(fd, &c, 1);
	} while (r < 0 && errno == EINTR);
	close(fd);
	while (waitpid(pid, NULL, 0) < 0 && errno == EINTR)
		;
	if (r != 1)
		return -1;
	return c == AUTH_OK ? 1 : 0;
}
