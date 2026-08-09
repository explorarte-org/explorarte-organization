package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/secrets"
)

// Adapter implements both embeddingruntime.OnlineAdapter and
// embeddingruntime.BatchAdapter against Google's Gemini embeddings REST
// surface. The exact REST paths below (embedContent/batchEmbedContents for
// the synchronous surface, asyncBatchEmbedContent/batches/* for the
// asynchronous Batch API) were confirmed against Google's live
// documentation at implementation time, not guessed — but Google's docs are
// not a stable contract this repo controls, so every path is isolated to a
// small set of constants/functions in this file and request.go to keep a
// future correction cheap and localized.
type Adapter struct {
	config  Config
	client  *http.Client
	breaker *circuitBreaker
	now     func() time.Time
}

func New(config Config) (*Adapter, error) {
	return newAdapter(config, nil, time.Now)
}

func newAdapter(config Config, client *http.Client, now func() time.Time) (*Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}
	base, err := url.Parse(config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse embeddingruntime gemini base url: %w", err)
	}
	base.Path = ""
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	if client == nil {
		client = defaultHTTPClient()
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("embeddingruntime gemini redirects are forbidden")
	}
	if now == nil {
		now = time.Now
	}
	config.BaseURL = base.String()
	return &Adapter{config: config, client: client, breaker: newCircuitBreaker(config.FailureThreshold, config.OpenDuration), now: now}, nil
}

var (
	_ embeddingruntime.OnlineAdapter = (*Adapter)(nil)
	_ embeddingruntime.BatchAdapter  = (*Adapter)(nil)
)

func (a *Adapter) token() ([]byte, error) {
	return secrets.LoadBearerToken(a.config.CredentialFile)
}

// doJSON is the shared HTTP plumbing for every method on Adapter: circuit
// breaker gate, bearer auth, bounded response read, and error
// classification. It never distinguishes "before send" from "ambiguous"
// from "response received" the way modelruntime's adapters must (there is
// no wallet reservation tied 1:1 to this call the way there is for chat
// dispatch — that distinction is the caller's responsibility via
// costledger's embedding_invocation path, see internal/costledger).
func (a *Adapter) doJSON(ctx context.Context, method, path string, body any, out any) (statusCode int, err error) {
	if !a.breaker.allow(a.now()) {
		return 0, fmt.Errorf("embeddingruntime gemini: circuit open")
	}
	var reader io.Reader
	if body != nil {
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return 0, fmt.Errorf("encode request: %w", marshalErr)
		}
		reader = bytes.NewReader(encoded)
	}
	requestCtx := ctx
	cancel := func() {}
	if a.config.RequestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, a.config.RequestTimeout)
	}
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, method, strings.TrimRight(a.config.BaseURL, "/")+path, reader)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	token, err := a.token()
	if err != nil {
		return 0, fmt.Errorf("load credential: %w", err)
	}
	defer secrets.Zero(token)
	httpRequest.Header.Set("Authorization", "Bearer "+string(token))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := a.client.Do(httpRequest)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			a.breaker.failure(a.now())
		}
		return 0, fmt.Errorf("embeddingruntime gemini: request failed (ambiguous — provider may or may not have received it): %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, int64(a.config.MaxResponseBytes)+1))
	if readErr != nil {
		a.breaker.failure(a.now())
		return response.StatusCode, fmt.Errorf("read response: %w", readErr)
	}
	if len(responseBody) > a.config.MaxResponseBytes {
		a.breaker.failure(a.now())
		return response.StatusCode, fmt.Errorf("response exceeds %d bytes", a.config.MaxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests {
			a.breaker.failure(a.now())
		}
		if isTextTooLongError(responseBody) {
			return response.StatusCode, embeddingruntime.ErrTextTooLong
		}
		return response.StatusCode, fmt.Errorf("embeddingruntime gemini: provider rejected request (status %d): %s", response.StatusCode, boundedPreview(responseBody))
	}
	a.breaker.success()
	if out != nil {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return response.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return response.StatusCode, nil
}

type providerErrorEnvelope struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// isTextTooLongError is a best-effort classification of Google's rejection
// for an over-limit input — it never causes this package to guess at a
// truncation, only at whether to surface embeddingruntime.ErrTextTooLong
// specifically instead of a generic rejection.
func isTextTooLongError(body []byte) bool {
	var envelope providerErrorEnvelope
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	message := strings.ToLower(envelope.Error.Message)
	return strings.Contains(message, "token") && (strings.Contains(message, "exceed") || strings.Contains(message, "too long") || strings.Contains(message, "limit"))
}

func boundedPreview(body []byte) string {
	const max = 500
	if len(body) > max {
		return string(body[:max])
	}
	return string(body)
}
