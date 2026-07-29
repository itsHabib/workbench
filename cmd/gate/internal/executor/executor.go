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
)

const (
	defaultAPIURL = "https://api.github.com"
	ghBinary      = "/usr/local/bin/gh"
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
}

// CommandResult is deliberately secret-free and output-free.
type CommandResult struct {
	ExitCode int
}

type commandRunner func(context.Context, []string, string) (CommandResult, error)

// Execute exchanges App custody for one short-lived installation token and
// runs argv byte-for-byte. It never returns or logs the token.
func Execute(ctx context.Context, config AppConfig, argv []string) (CommandResult, error) {
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return execute(ctx, config, argv, &client, runGH)
}

func execute(ctx context.Context, config AppConfig, argv []string, client *http.Client, runner commandRunner) (CommandResult, error) {
	if err := validateArgv(argv); err != nil {
		return CommandResult{}, err
	}
	token, err := exchange(ctx, config, client, time.Now)
	if err != nil {
		return CommandResult{}, err
	}
	result, err := runner(ctx, append([]string(nil), argv...), token.value)
	token.clear()
	if err != nil {
		return result, fmt.Errorf("%w: exit %d", ErrCommand, result.ExitCode)
	}
	return result, nil
}

type installationToken struct {
	value     string
	expiresAt time.Time
}

func (token *installationToken) clear() {
	token.value = ""
}

func exchange(ctx context.Context, config AppConfig, client *http.Client, now func() time.Time) (installationToken, error) {
	if config.AppID < 1 || config.InstallationID < 1 || len(config.PrivateKeyPEM) == 0 {
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
	body := strings.NewReader(`{"permissions":{"contents":"write"}}`)
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
	return installationToken{value: payload.Token, expiresAt: payload.ExpiresAt}, nil
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
	if err != nil || number < 1 || !validRepo(argv[5]) || !validSHA(argv[9]) {
		return errors.New("executor_argv_invalid")
	}
	for _, arg := range argv {
		if arg == "--admin" {
			return errors.New("executor_admin_forbidden")
		}
	}
	return nil
}

func validRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" &&
		!strings.ContainsAny(repo, " \t\r\n")
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
	var stderr bytes.Buffer
	command.Stdout = io.Discard
	command.Stderr = &limitedWriter{writer: &stderr, remaining: 4096}
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

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *limitedWriter) Write(data []byte) (int, error) {
	original := len(data)
	if writer.remaining <= 0 {
		return original, nil
	}
	if int64(len(data)) > writer.remaining {
		data = data[:writer.remaining]
	}
	n, err := writer.writer.Write(data)
	writer.remaining -= int64(n)
	if err != nil {
		return n, err
	}
	return original, nil
}
