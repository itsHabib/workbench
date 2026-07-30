// Package executor owns GitHub App credential custody and exact-command
// execution. The App private key enters this process; the installation token
// exists only in memory here and in the direct gh child process.
package executor

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultAPIURL = "https://api.github.com"
	ghBinary      = "gh"
	maxResponse   = 64 * 1024
)

var (
	// ErrCredentials reports malformed or absent App custody.
	ErrCredentials = errors.New("executor_credentials_invalid")
	// ErrToken reports a failed or invalid installation-token exchange.
	ErrToken = errors.New("executor_token_exchange_failed")
	// ErrCommand reports a refused exact merge command.
	ErrCommand = errors.New("executor_command_failed")
)

// AppConfig is the private input accepted only by the executor process.
type AppConfig struct {
	AppID          int64
	InstallationID int64
	PrivateKeyPEM  []byte
	APIURL         string
	Repository     string
}

// CommandResult is deliberately secret-free and output-free.
type CommandResult struct {
	ExitCode int
}

type commandRunner func(context.Context, []string, string) (CommandResult, error)

// Session keeps the installation token inside Gate while one approved
// execution publishes state and runs the exact merge command.
type Session struct {
	client *http.Client
	config AppConfig
	token  installationToken
}

// WithSession exchanges App custody once, runs fn, and clears the token before
// returning. The token is never returned to the caller.
func WithSession(ctx context.Context, config AppConfig, fn func(*Session) error) error {
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return withSession(ctx, config, &client, fn)
}

func withSession(ctx context.Context, config AppConfig, client *http.Client, fn func(*Session) error) error {
	if fn == nil {
		return errors.New("executor session callback required")
	}
	token, err := exchange(ctx, config, client, time.Now)
	if err != nil {
		return err
	}
	session := &Session{client: client, config: config, token: token}
	defer session.token.clear()
	return fn(session)
}

// Merge runs the stored argv byte-for-byte with the custodied token.
func (session *Session) Merge(ctx context.Context, argv []string) (CommandResult, error) {
	return session.merge(ctx, argv, runGH)
}

func (session *Session) merge(ctx context.Context, argv []string, runner commandRunner) (CommandResult, error) {
	if err := validateArgv(argv); err != nil {
		return CommandResult{}, err
	}
	result, err := runner(ctx, append([]string(nil), argv...), session.token.value)
	if err != nil {
		return result, fmt.Errorf("%w: exit %d", ErrCommand, result.ExitCode)
	}
	return result, nil
}

type installationToken struct {
	value string
}

func (token *installationToken) clear() {
	token.value = ""
}

func exchange(ctx context.Context, config AppConfig, client *http.Client, now func() time.Time) (installationToken, error) {
	if config.AppID < 1 || config.InstallationID < 1 || len(config.PrivateKeyPEM) == 0 ||
		!ValidRepository(config.Repository) {
		return installationToken{}, ErrCredentials
	}
	apiURL := strings.TrimRight(config.APIURL, "/")
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	jwt, err := signJWT(config.AppID, config.PrivateKeyPEM, now())
	if err != nil {
		return installationToken{}, fmt.Errorf("%w: %v", ErrCredentials, err)
	}
	repository := strings.Split(config.Repository, "/")[1]
	requestData, err := json.Marshal(map[string]any{
		"repositories": []string{repository},
		"permissions":  map[string]string{"contents": "write"},
	})
	if err != nil {
		return installationToken{}, fmt.Errorf("%w: encode request", ErrToken)
	}
	body := bytes.NewReader(requestData)
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiURL, config.InstallationID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return installationToken{}, fmt.Errorf("%w: build request: %v", ErrToken, err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+jwt)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.Do(request)
	if err != nil {
		return installationToken{}, fmt.Errorf("%w: request: %v", ErrToken, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return installationToken{}, fmt.Errorf("%w: read response: %v", ErrToken, err)
	}
	if response.StatusCode != http.StatusCreated {
		return installationToken{}, fmt.Errorf("%w: HTTP %d", ErrToken, response.StatusCode)
	}
	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return installationToken{}, fmt.Errorf("%w: malformed response", ErrToken)
	}
	current := now().UTC()
	if !validToken(payload.Token) || !payload.ExpiresAt.After(current) ||
		payload.ExpiresAt.After(current.Add(65*time.Minute)) {
		return installationToken{}, fmt.Errorf("%w: invalid token bounds", ErrToken)
	}
	return installationToken{value: payload.Token}, nil
}

func validToken(token string) bool {
	if token == "" || len(token) > 4096 {
		return false
	}
	for _, r := range token {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func signJWT(appID int64, keyData []byte, now time.Time) (string, error) {
	key, err := parsePrivateKey(keyData)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(struct {
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		Issuer    string `json:"iss"`
	}{
		IssuedAt:  now.Add(-30 * time.Second).Unix(),
		ExpiresAt: now.Add(9 * time.Minute).Unix(),
		Issuer:    strconv.FormatInt(appID, 10),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private key is not RSA PKCS1 or PKCS8")
	}
	key, ok := value.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}

func validateArgv(argv []string) error {
	if len(argv) != 10 || argv[0] != "gh" || argv[1] != "pr" ||
		argv[2] != "merge" || argv[4] != "-R" || argv[6] != "--squash" ||
		argv[7] != "--delete-branch" || argv[8] != "--match-head-commit" {
		return errors.New("executor_argv_invalid")
	}
	number, err := strconv.Atoi(argv[3])
	if err != nil || number < 1 || !ValidRepository(argv[5]) || !validSHA(argv[9]) {
		return errors.New("executor_argv_invalid")
	}
	for _, arg := range argv {
		if arg == "--admin" {
			return errors.New("executor_admin_forbidden")
		}
	}
	return nil
}

// ValidRepository reports whether repo is a canonical owner/name identity.
func ValidRepository(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	if repo != strings.TrimSpace(repo) || strings.IndexFunc(repo, unicode.IsSpace) >= 0 {
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." ||
			strings.HasPrefix(part, "-") || strings.HasSuffix(part, ".git") {
			return false
		}
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) ||
				strings.ContainsRune("._-", r) {
				continue
			}
			return false
		}
	}
	return true
}

func validSHA(sha string) bool {
	if len(sha) != 40 || sha != strings.ToLower(sha) {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func runGH(ctx context.Context, argv []string, token string) (CommandResult, error) {
	command := exec.CommandContext(ctx, ghBinary, argv[1:]...)
	command.Env = childEnvironment(token)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	err := command.Run()
	if err == nil {
		return CommandResult{ExitCode: 0}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return CommandResult{ExitCode: exitErr.ExitCode()}, ErrCommand
	}
	return CommandResult{ExitCode: -1}, ErrCommand
}

func childEnvironment(token string) []string {
	return []string{
		"GH_TOKEN=" + token,
		"GH_PROMPT_DISABLED=1",
		"HOME=/tmp",
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
}
