package frappe

import (
	"fmt"

	"github.com/zhide915/tamp/internal/gitcred"
)

// The bridged credential's env var names. The password var is exported so the
// engine fake can gate its private repositories on the same contract.
const (
	credUsernameVar       = "TAMP_GIT_USERNAME"
	CredentialPasswordVar = "TAMP_GIT_PASSWORD"
)

// CredentialEnv is the in-container half (ADR 0002): an env-fed helper for url
// alone. With host set, it declines what the replayed httpPath scopes by path.
func CredentialEnv(url, username, password string, host bool, httpPath []gitcred.Setting) []string {
	scope := "credential." + gitcred.ConfigURL(url) + ".helper"
	helper := envHelper
	if host {
		helper = hostHelper
	}
	config := append(httpPath[:len(httpPath):len(httpPath)],
		// The empty value first resets any configured helper list, so none
		// can store the secret at rest.
		gitcred.Setting{Key: scope, Value: ""},
		gitcred.Setting{Key: scope, Value: helper})
	env := []string{
		"GIT_TERMINAL_PROMPT=0",
		credUsernameVar + "=" + username,
		CredentialPasswordVar + "=" + password,
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(config)),
	}
	for i, c := range config {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, c.Key),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, c.Value))
	}
	return env
}

// envHelper answers only "get". The host completes store and erase.
const envHelper = `!f() { if [ "$1" = get ]; then printf 'username=%s\npassword=%s\n' "$TAMP_GIT_USERNAME" "$TAMP_GIT_PASSWORD"; fi; }; f`

// hostHelper is envHelper for a host credential: git passes it a path only
// where useHttpPath scopes the URL by path, so it declines those requests.
const hostHelper = `!f() { [ "$1" = get ] || return 0; while read -r l; do case "$l" in path=*) return 0;; esac; done; printf 'username=%s\npassword=%s\n' "$TAMP_GIT_USERNAME" "$TAMP_GIT_PASSWORD"; }; f`
