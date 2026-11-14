// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"encoding/base64"
	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
)

type HostedRuntime struct {
	Store         *store.Store
	Vault         *hosted.Vault
	WorkerToken   string
	PublicAPIBase string
	Configured    bool
	HTTPClient    *http.Client
}

func NewHostedRuntime(st *store.Store, key []byte, workerToken, publicAPIBase string) (*HostedRuntime, error) {
	r := &HostedRuntime{Store: st}
	u, err := url.Parse(publicAPIBase)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || len(workerToken) < 32 || st == nil {
		return nil, hosted.ErrUnavailable
	}
	vault, err := hosted.NewVault(key)
	if err != nil {
		return nil, err
	}
	r.Configured = true
	r.Vault = vault
	r.WorkerToken = workerToken
	r.PublicAPIBase = strings.TrimRight(publicAPIBase, "/")
	r.HTTPClient = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return r, nil
}
func (a *API) hostedReady(w http.ResponseWriter) bool {
	if a.Hosted == nil || !a.Hosted.Configured || a.Hosted.Vault == nil || a.Hosted.Store == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, httpx.ErrorBody{Error: "execution_unavailable", Message: "hosted execution is not configured"})
		return false
	}
	return true
}
func (a *API) hostedHuman(w http.ResponseWriter, r *http.Request) (store.Tenant, bool) {
	t, ok := tenant(r.Context())
	if !ok {
		httpx.WriteError(w, wrapUnauthorized())
		return t, false
	}
	if !humanSession(r) {
		writeHostedError(w, hosted.ErrDenied)
		return t, false
	}
	if !a.hostedReady(w) || !a.requireStore(w) || !a.requireOrgMember(w, r, t) {
		return t, false
	}
	return t, true
}
func writeHostedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hosted.ErrDenied), errors.Is(err, store.ErrNoMembership), errors.Is(err, store.ErrRoomForbidden):
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorBody{Error: "execution_denied", Message: "execution authorization is no longer valid"})
	case errors.Is(err, hosted.ErrConflict):
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorBody{Error: "execution_conflict", Message: "execution or baseline changed"})
	case errors.Is(err, hosted.ErrBudget):
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorBody{Error: "budget_exhausted", Message: "execution limit reached"})
	case errors.Is(err, hosted.ErrInvalid):
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorBody{Error: "invalid_execution", Message: "invalid execution request"})
	default:
		writeStoreError(w, err)
	}
}
func decodeHosted(w http.ResponseWriter, r *http.Request, out any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		writeHostedError(w, hosted.ErrInvalid)
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		writeHostedError(w, hosted.ErrInvalid)
		return false
	}
	return true
}
func (a *API) ListProviderCredentials(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	credentials, err := a.Hosted.Store.ListProviderCredentials(r.Context(), t)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"credentials": credentials, "execution_enabled": true})
}
func (a *API) SaveProviderCredential(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Scope    string `json:"scope"`
		Label    string `json:"label"`
		APIKey   string `json:"api_key"`
	}
	if !decodeHosted(w, r, &body, 8192) {
		return
	}
	credential, err := a.Hosted.Store.SaveProviderCredential(store.WithHumanSession(r.Context(), true), t, chi.URLParam(r, "credentialID"), body.Provider, body.Label, body.APIKey, a.Hosted.Vault, body.Scope)
	body.APIKey = ""
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"credential": credential})
}
func (a *API) RevokeProviderCredential(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	if err := a.Hosted.Store.RevokeProviderCredential(store.WithHumanSession(r.Context(), true), t, chi.URLParam(r, "credentialID")); err != nil {
		writeHostedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) ValidateProviderCredential(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "credentialID")
	provider, secret, generation, err := a.Hosted.Store.ProviderCredentialSecret(r.Context(), t, id, a.Hosted.Vault)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	endpoint := "https://api.anthropic.com/v1/models"
	if provider == "openai" {
		endpoint = "https://api.openai.com/v1/models"
	}
	if provider == "openrouter" {
		endpoint = "https://openrouter.ai/api/v1/key"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	setProviderHeaders(req, provider, secret)
	resp, err := a.Hosted.HTTPClient.Do(req)
	secret = ""
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, httpx.ErrorBody{Error: "provider_unavailable", Message: "provider validation failed"})
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorBody{Error: "credential_invalid", Message: "provider rejected this credential"})
		return
	}
	if err := a.Hosted.Store.ValidateProviderCredential(r.Context(), t, id, generation); err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"validated": true})
}
func setProviderHeaders(r *http.Request, provider, secret string) {
	r.Header.Set("Content-Type", "application/json")
	if provider == "anthropic" {
		r.Header.Set("x-api-key", secret)
		r.Header.Set("anthropic-version", "2023-06-01")
	} else {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
}

func (a *API) QueueHostedExecution(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	var body struct {
		SiteID         string        `json:"site_id"`
		VersionID      string        `json:"version_id"`
		CredentialRef  string        `json:"credential_ref"`
		Provider       string        `json:"provider"`
		ModelID        string        `json:"model_id"`
		Prompt         string        `json:"prompt"`
		IdempotencyKey string        `json:"idempotency_key"`
		ThreadID       string        `json:"thread_id"`
		Budget         hosted.Budget `json:"budget"`
	}
	if !decodeHosted(w, r, &body, 32768) {
		return
	}
	job, err := a.Hosted.Store.QueueExecution(store.WithHumanSession(r.Context(), true), t, chi.URLParam(r, "roomID"), body.SiteID, body.VersionID, body.CredentialRef, body.IdempotencyKey, hosted.Config{Provider: body.Provider, ModelID: body.ModelID, Prompt: body.Prompt, ThreadID: body.ThreadID, Budget: body.Budget})
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"job": job})
}
func (a *API) ListHostedExecutions(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	jobs, err := a.Hosted.Store.ListExecutionJobs(r.Context(), t, chi.URLParam(r, "roomID"))
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}
func (a *API) GetHostedExecution(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	detail, err := a.Hosted.Store.GetExecutionJob(r.Context(), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "jobID"))
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}
func (a *API) CancelHostedExecution(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	if err := a.Hosted.Store.CancelExecutionJob(store.WithHumanSession(r.Context(), true), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "jobID")); err != nil {
		writeHostedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) ResumeHostedExecution(w http.ResponseWriter, r *http.Request) {
	t, ok := a.hostedHuman(w, r)
	if !ok {
		return
	}
	var body struct {
		Answer string `json:"answer"`
		Key    string `json:"idempotency_key"`
	}
	if !decodeHosted(w, r, &body, 32768) {
		return
	}
	job, err := a.Hosted.Store.ResumeExecution(store.WithHumanSession(r.Context(), true), t, chi.URLParam(r, "roomID"), chi.URLParam(r, "sessionID"), body.Answer, body.Key)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (a *API) ClaimHostedWork(w http.ResponseWriter, r *http.Request) {
	if !a.hostedReady(w) {
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.Hosted.WorkerToken)) != 1 {
		writeHostedError(w, hosted.ErrDenied)
		return
	}
	var body struct {
		WorkerID string `json:"worker_id"`
	}
	if !decodeHosted(w, r, &body, 1024) {
		return
	}
	if len(body.WorkerID) < 1 || len(body.WorkerID) > 100 {
		writeHostedError(w, hosted.ErrInvalid)
		return
	}
	claim, err := a.Hosted.Store.ClaimExecutionJob(r.Context(), body.WorkerID)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	if claim == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		*store.ExecutionClaim
		GatewayBaseURL string `json:"gateway_base_url"`
	}{claim, a.Hosted.PublicAPIBase + "/internal/room-gateway/" + claim.AttemptID})
}
func (a *API) attemptToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !a.hostedReady(w) {
		return "", false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_, attempt, err := hosted.ParseToken(token, "attempt")
	if err != nil || attempt != chi.URLParam(r, "attemptID") {
		writeHostedError(w, hosted.ErrDenied)
		return "", false
	}
	return token, true
}
func (a *API) HeartbeatHostedAttempt(w http.ResponseWriter, r *http.Request) {
	token, ok := a.attemptToken(w, r)
	if !ok {
		return
	}
	lease, err := a.Hosted.Store.HeartbeatExecution(r.Context(), token)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"lease_expires_at": lease})
}
func (a *API) HostedAttemptSource(w http.ResponseWriter, r *http.Request) {
	token, ok := a.attemptToken(w, r)
	if !ok || !a.requireObjects(w) {
		return
	}
	job, files, err := a.Hosted.Store.ExecutionSource(r.Context(), token, a.Objects)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	entries := make([]manifest.File, 0, len(files))
	for i := range files {
		data, _ := base64.StdEncoding.DecodeString(files[i].ContentBase64)
		files[i].SHA256 = hosted.Hash(string(data))
		files[i].Size = int64(len(data))
		entries = append(entries, manifest.File{Path: files[i].Path, SHA256: files[i].SHA256})
	}
	response := map[string]any{"files": files, "content_hash": job.ContentHash, "version_id": job.VersionID, "resumed": job.SessionID != nil, "source_content_hash": manifest.Digest(entries)}
	if job.Config.ThreadID != "" {
		thread, err := a.Hosted.Store.PageReviewReplies(r.Context(), store.Tenant{OrgID: job.OrgID, UserID: job.InitiatorUserID}, job.Config.ThreadID, "", 50)
		if err != nil {
			writeHostedError(w, err)
			return
		}
		response["thread"] = thread
		response["thread_content"] = "untrusted_data"
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}
func (a *API) HostedAttemptCandidate(w http.ResponseWriter, r *http.Request) {
	token, ok := a.attemptToken(w, r)
	if !ok || !a.requireObjects(w) {
		return
	}
	var body struct {
		Files   []hosted.File `json:"files"`
		Summary string        `json:"summary"`
		Key     string        `json:"idempotency_key"`
	}
	if !decodeHosted(w, r, &body, 15<<20) {
		return
	}
	candidate, err := a.Hosted.Store.CompleteExecutionCandidate(r.Context(), token, body.Key, body.Summary, body.Files, a.Objects)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"candidate": candidateResponse(candidate)})
}
func (a *API) HostedAttemptClarify(w http.ResponseWriter, r *http.Request) {
	token, ok := a.attemptToken(w, r)
	if !ok {
		return
	}
	var body struct {
		Question   string          `json:"question"`
		Transcript json.RawMessage `json:"transcript"`
		Files      []hosted.File   `json:"files"`
	}
	if !decodeHosted(w, r, &body, 15<<20) {
		return
	}
	id, err := a.Hosted.Store.ClarifyExecution(r.Context(), token, body.Question, body.Transcript, body.Files)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"session_id": id})
}
func (a *API) HostedAttemptEvent(w http.ResponseWriter, r *http.Request) {
	token, ok := a.attemptToken(w, r)
	if !ok {
		return
	}
	var body struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	if !decodeHosted(w, r, &body, 8192) {
		return
	}
	if err := a.Hosted.Store.AppendExecutionEvent(r.Context(), token, body.Kind, body.Message); err != nil {
		writeHostedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) HostedAttemptFail(w http.ResponseWriter, r *http.Request) {
	token, ok := a.attemptToken(w, r)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !decodeHosted(w, r, &body, 1024) {
		return
	}
	if err := a.Hosted.Store.FailExecution(r.Context(), token, body.Code); err != nil {
		writeHostedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
