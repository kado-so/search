package searchclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
	"unicode/utf16"
)

func TestRunSuccessAndGloballyCachedRepeatAreObservable(t *testing.T) {
	t.Parallel()

	var mutex sync.Mutex
	providerExecutions := 0
	requests := 0
	cached := map[string]bool{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		query := request.URL.Query().Get("q")
		mutex.Lock()
		requests++
		if !cached[query] {
			cached[query] = true
			providerExecutions++
		}
		mutex.Unlock()
		writeDocument(response, completeDocument(serverURL(request), query, "search_cache", "", ""))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	options := DefaultRunOptions()
	options.FollowPages = false
	for range 2 {
		result, err := client.Run(context.Background(), "cached tools", options)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if result.Document.Status != StatusComplete || len(result.Pages) != 1 {
			t.Fatalf("Run() result = %#v", result)
		}
	}
	mutex.Lock()
	defer mutex.Unlock()
	if requests != 2 || providerExecutions != 1 {
		t.Fatalf("requests=%d provider executions=%d", requests, providerExecutions)
	}
}

func TestSearchCanonicalizesUnicodeAndHumanWhitespaceLikeKadoApp(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		if query := request.URL.Query().Get("q"); query != "café tools for agents" {
			t.Fatalf("query = %q", query)
		}
		writeDocument(response, completeDocument(
			serverURL(request),
			"café tools for agents",
			"search_canonical_query",
			"",
			"",
		))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	document, err := client.Search(
		context.Background(),
		"\u00a0cafe\u0301\ttools\nfor\u3000agents  ",
	)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if document.Query != "café tools for agents" {
		t.Fatalf("document query = %q", document.Query)
	}
}

func TestRunPollsLifecycleThroughServerSelfLink(t *testing.T) {
	t.Parallel()

	statusCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		self := serverURL(request) + "/search?q=lifecycle+tools"
		if request.URL.Query().Get("operation") == "" {
			writeDocument(response, lifecycleDocument(self, "lifecycle tools", "search_lifecycle", StatusQueued))
			return
		}
		if request.URL.Query().Get("operation") != "status" ||
			request.URL.Query().Get("search_id") != "search_lifecycle" {
			t.Fatalf("status request query = %q", request.URL.RawQuery)
		}
		statusCalls++
		status := StatusRunning
		if statusCalls == 2 {
			status = StatusComplete
		}
		if status == StatusComplete {
			writeDocument(response, completeDocument(serverURL(request), "lifecycle tools", "search_lifecycle", "", ""))
		} else {
			writeDocument(response, lifecycleDocument(self, "lifecycle tools", "search_lifecycle", status))
		}
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	client.wait = noWait
	options := DefaultRunOptions()
	options.FollowPages = false
	result, err := client.Run(context.Background(), "lifecycle tools", options)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Document.Status != StatusComplete || statusCalls != 2 {
		t.Fatalf("status=%s calls=%d", result.Document.Status, statusCalls)
	}
}

func TestRunPollsProductDocumentThroughPrivateLifecycleEndpoint(t *testing.T) {
	t.Parallel()

	statusCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		publicSelf := serverURL(request) +
			"/search/search_public_lifecycle/opaque_token_1"
		if request.URL.Query().Get("operation") == "" {
			writeDocument(response, lifecycleDocument(
				publicSelf,
				"lifecycle path",
				"search_public_lifecycle",
				StatusQueued,
			))
			return
		}
		if request.URL.Path != "/search" ||
			request.URL.Query().Get("q") != "lifecycle path" ||
			request.URL.Query().Get("operation") != "status" ||
			request.URL.Query().Get("search_id") != "search_public_lifecycle" {
			t.Fatalf("status request URL = %q", request.URL.String())
		}
		statusCalls++
		writeDocument(response, completeDocument(
			serverURL(request),
			"lifecycle path",
			"search_public_lifecycle",
			"",
			"",
		))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	client.wait = noWait
	options := DefaultRunOptions()
	options.FollowPages = false
	result, err := client.Run(context.Background(), "lifecycle path", options)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Document.Status != StatusComplete || statusCalls != 1 {
		t.Fatalf("status=%s calls=%d", result.Document.Status, statusCalls)
	}
}

func TestRunSubmitsBoundedClarification(t *testing.T) {
	t.Parallel()

	clarifications := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		self := serverURL(request) + "/search?q=clarify+tools"
		if request.Method == http.MethodGet {
			writeDocument(response, needsInputDocument(self, "clarify tools", "search_clarify"))
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		clarifications++
		if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
			request.PostForm.Get("operation") != "clarify" ||
			request.PostForm.Get("q") != "clarify tools" ||
			request.PostForm.Get("search_id") != "search_clarify" ||
			request.PostForm.Get("question_id") != "question_platform" ||
			request.PostForm.Get("answer") != "Web" {
			t.Fatalf("clarification form = %#v", request.PostForm)
		}
		writeDocument(response, completeDocument(serverURL(request), "clarify tools", "search_clarify", "", ""))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	options := DefaultRunOptions()
	options.FollowPages = false
	options.Clarify = func(_ context.Context, question Question) (string, error) {
		if question.ID != "question_platform" ||
			question.Prompt != "Which platform?" ||
			len(question.Options) != 3 {
			t.Fatalf("question = %#v", question)
		}
		return "Web", nil
	}
	result, err := client.Run(context.Background(), "clarify tools", options)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Document.Status != StatusComplete || clarifications != 1 {
		t.Fatalf("status=%s clarifications=%d", result.Document.Status, clarifications)
	}
}

func TestRunFollowsOpaquePaginationLinkExactly(t *testing.T) {
	t.Parallel()

	var followed string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		if request.URL.Query().Get("cursor") == "" {
			next := serverURL(request) + "/search?q=page+tools&cursor=opaque_2"
			writeDocument(response, completeDocument(
				serverURL(request),
				"page tools",
				"search_pages",
				next,
				"",
			))
			return
		}
		followed = request.URL.String()
		writeDocument(response, completeDocument(serverURL(request), "page tools", "search_pages", "", ""))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	result, err := client.Run(context.Background(), "page tools", DefaultRunOptions())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "/search?q=page+tools&cursor=opaque_2"
	if len(result.Pages) != 2 || followed != want {
		t.Fatalf("pages=%d followed=%q want=%q", len(result.Pages), followed, want)
	}
}

func TestRunFollowsCanonicalPublicPaginationLinkExactly(t *testing.T) {
	t.Parallel()

	var followed string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		if request.URL.Path == "/search" {
			next := serverURL(request) + "/search/public_ref_2/page%20tools?page=2"
			writeDocument(response, completeDocument(
				serverURL(request),
				"page tools",
				"search_public_pages",
				next,
				"",
			))
			return
		}
		followed = request.URL.String()
		writeDocument(response, completeDocument(
			serverURL(request),
			"page tools",
			"search_public_pages",
			"",
			"",
		))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	result, err := client.Run(context.Background(), "page tools", DefaultRunOptions())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "/search/public_ref_2/page%20tools?page=2"
	if len(result.Pages) != 2 || followed != want {
		t.Fatalf("pages=%d followed=%q want=%q", len(result.Pages), followed, want)
	}
}

func TestCancelUsesExactDocumentIdentity(t *testing.T) {
	t.Parallel()

	cancelCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		self := serverURL(request) + "/search?q=cancel+tools"
		if request.Method == http.MethodGet {
			writeDocument(response, lifecycleDocument(self, "cancel tools", "search_cancel", StatusRunning))
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		cancelCalls++
		if request.PostForm.Get("operation") != "cancel" ||
			request.PostForm.Get("search_id") != "search_cancel" {
			t.Fatalf("cancel form = %#v", request.PostForm)
		}
		writeDocument(response, lifecycleDocument(self, "cancel tools", "search_cancel", StatusCanceled))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	document, err := client.Search(context.Background(), "cancel tools")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	canceled, err := client.Cancel(context.Background(), document)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if canceled.Status != StatusCanceled || cancelCalls != 1 {
		t.Fatalf("status=%s cancel calls=%d", canceled.Status, cancelCalls)
	}
}

func TestRunTimeoutAttemptsBoundedServerCancellation(t *testing.T) {
	t.Parallel()

	cancelCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		self := serverURL(request) + "/search?q=slow+tools"
		if request.Method == http.MethodPost {
			cancelCalls++
			writeDocument(response, lifecycleDocument(self, "slow tools", "search_slow", StatusCanceled))
			return
		}
		writeDocument(response, lifecycleDocument(self, "slow tools", "search_slow", StatusRunning))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	client.wait = func(context.Context, time.Duration) error {
		return context.DeadlineExceeded
	}
	options := DefaultRunOptions()
	options.Timeout = time.Minute
	_, err := client.Run(context.Background(), "slow tools", options)
	if !errors.Is(err, ErrTimeout) || cancelCalls != 1 {
		t.Fatalf("Run() error=%v cancel calls=%d", err, cancelCalls)
	}
}

func TestSearchRefreshesAuthenticationExactlyOnceAfter401(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		requests++
		if requests == 1 {
			if request.Header.Get("Authorization") != "Bearer token-one" {
				t.Fatalf("first authorization = %q", request.Header.Get("Authorization"))
			}
			writeProblem(response, http.StatusUnauthorized, "authentication_required", false)
			return
		}
		assertMachineRequest(t, request, "Bearer token-two")
		writeDocument(response, completeDocument(serverURL(request), "auth tools", "search_auth", "", ""))
	}))
	defer server.Close()

	source := &fakeAuthorizationSource{}
	client := newIntegrationClient(t, server, source)
	if _, err := client.Search(context.Background(), "auth tools"); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if source.calls != 2 || source.refreshes != 1 || requests != 2 {
		t.Fatalf(
			"authorization calls=%d refreshes=%d requests=%d",
			source.calls,
			source.refreshes,
			requests,
		)
	}
}

func TestSearchRetriesOnlySafeGETFailures(t *testing.T) {
	t.Parallel()

	getCalls := 0
	postCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		if request.Method == http.MethodGet {
			getCalls++
			if getCalls == 1 {
				response.Header().Set("Retry-After", "0")
				writeProblem(response, http.StatusServiceUnavailable, "temporarily_unavailable", true)
				return
			}
			self := serverURL(request) + "/search?q=retry+tools"
			writeDocument(response, needsInputDocument(self, "retry tools", "search_retry"))
			return
		}
		postCalls++
		writeProblem(response, http.StatusServiceUnavailable, "temporarily_unavailable", true)
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	client.wait = noWait
	document, err := client.Search(context.Background(), "retry tools")
	if err != nil || document.Status != StatusNeedsInput || getCalls != 2 {
		t.Fatalf("Search() status=%s error=%v get calls=%d", document.Status, err, getCalls)
	}
	_, err = client.Clarify(context.Background(), document, "Web")
	var remote *Error
	if !errors.As(err, &remote) ||
		remote.Code() != "temporarily_unavailable" ||
		!remote.Retryable() ||
		postCalls != 1 {
		t.Fatalf("Clarify() error=%v post calls=%d", err, postCalls)
	}
}

func TestRunCanRetryOneStructuredRetryableFailure(t *testing.T) {
	t.Parallel()

	retryCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		self := serverURL(request) + "/search?q=recover+tools"
		if request.Method == http.MethodGet {
			writeDocument(response, failedDocument(
				self,
				"recover tools",
				"search_recover",
				"SOURCE_UNAVAILABLE",
				"A source was unavailable.",
				true,
			))
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		retryCalls++
		if request.PostForm.Get("operation") != "retry" {
			t.Fatalf("retry operation = %q", request.PostForm.Get("operation"))
		}
		writeDocument(response, completeDocument(
			serverURL(request),
			"recover tools",
			"search_recover",
			"",
			"",
		))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	options := DefaultRunOptions()
	options.FollowPages = false
	options.RetryFailure = true
	result, err := client.Run(context.Background(), "recover tools", options)
	if err != nil || result.Document.Status != StatusComplete || retryCalls != 1 {
		t.Fatalf("Run() status=%s error=%v retry calls=%d", result.Document.Status, err, retryCalls)
	}
}

func TestRunNeedsInputIsStructuredWhenNoClarifierExists(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		self := serverURL(request) + "/search?q=question+tools"
		writeDocument(response, needsInputDocument(self, "question tools", "search_question"))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	result, err := client.Run(context.Background(), "question tools", DefaultRunOptions())
	var needsInput *NeedsInputError
	if !errors.As(err, &needsInput) ||
		result.Document.Status != StatusNeedsInput ||
		needsInput.Question.ID != "question_platform" {
		t.Fatalf("Run() result=%#v error=%T %v", result, err, err)
	}
}

func TestRunReturnsBoundedStructuredFailureWithoutRemoteSecrets(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		assertMachineRequest(t, request, "Bearer token-one")
		self := serverURL(request) + "/search?q=failing+tools"
		writeDocument(response, failedDocument(
			self,
			"failing tools",
			"search_failure",
			"SOURCE_UNAVAILABLE",
			"A required public source was unavailable.",
			true,
		))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	result, err := client.Run(context.Background(), "failing tools", DefaultRunOptions())
	var failure *FailureError
	if !errors.As(err, &failure) {
		t.Fatalf("Run() error = %T %v", err, err)
	}
	if result.Document.Status != StatusFailed ||
		failure.Failure.Code != "source_unavailable" ||
		failure.Failure.Message != "A required public source was unavailable." ||
		!failure.Failure.Retryable {
		t.Fatalf("result=%#v failure=%#v", result, failure)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", err, err), "Bearer ") {
		t.Fatalf("failure formatting exposed authorization")
	}
}

func TestServerDerivedFailureAndProblemDiagnosticsAreTerminalSafe(t *testing.T) {
	t.Parallel()

	const unsafeMessage = "before\u001b\u0085\u009b\u2028\u2029\u202e\u2066after Café 世界 🧭"
	const safeMessage = "before after Café 世界 🧭"
	t.Run("Search Document failure", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			assertMachineRequest(t, request, "Bearer token-one")
			self := serverURL(request) + "/search?q=failing+tools"
			writeDocument(response, failedDocument(
				self,
				"failing tools",
				"search_failure",
				"SOURCE_UNAVAILABLE",
				unsafeMessage,
				false,
			))
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		_, err := client.Run(
			context.Background(),
			"failing tools",
			DefaultRunOptions(),
		)
		var failure *FailureError
		if !errors.As(err, &failure) ||
			failure.Failure.Message != safeMessage ||
			strings.ContainsFunc(failure.Error(), unsafeDiagnosticRune) {
			t.Fatalf("Run() failure = %#v error=%T %q", failure, err, err)
		}
	})

	t.Run("HTTP problem", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			assertMachineRequest(t, request, "Bearer token-one")
			response.Header().Set("Content-Type", "application/problem+json")
			response.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(response).Encode(map[string]any{
				"error": map[string]any{
					"code":      "rate_limited",
					"message":   unsafeMessage,
					"retryable": true,
				},
			})
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		_, err := client.Search(context.Background(), "agent tools")
		var problem *Error
		if !errors.As(err, &problem) ||
			problem.Code() != "rate_limited" ||
			problem.Error() != safeMessage ||
			!problem.Retryable() ||
			strings.ContainsFunc(problem.Error(), unsafeDiagnosticRune) {
			t.Fatalf("Search() problem = %#v error=%T %q", problem, err, err)
		}
	})
}

func TestClientRejectsOversizedBodiesWrongMediaAndForeignLinks(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "oversized",
			handler: func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", CanonicalMediaType)
				_, _ = response.Write([]byte(strings.Repeat("x", 64*1024+1)))
			},
		},
		{
			name: "wrong media",
			handler: func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(response).Encode(
					completeDocument(serverURL(request), "tools", "search_media", "", ""),
				)
			},
		},
		{
			name: "foreign link",
			handler: func(response http.ResponseWriter, request *http.Request) {
				document := completeDocument(serverURL(request), "tools", "search_link", "", "")
				document["links"].(map[string]any)["self"] = "https://evil.invalid/search?q=tools"
				writeDocument(response, document)
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(test.handler)
			defer server.Close()
			limits := DefaultLimits()
			limits.MaxDocumentBytes = 64 * 1024
			client := newIntegrationClientWithLimits(
				t,
				server,
				&fakeAuthorizationSource{},
				limits,
			)
			_, err := client.Search(context.Background(), "tools")
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("Search() error = %T %v", err, err)
			}
		})
	}
}

func TestClientRejectsRedirectsWithoutForwardingAuthorization(t *testing.T) {
	t.Parallel()

	targetCalls := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(
		_ http.ResponseWriter,
		request *http.Request,
	) {
		targetCalls++
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("redirect target received authorization")
		}
	}))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		http.Redirect(response, request, target.URL+"/search?q=tools", http.StatusFound)
	}))
	defer redirect.Close()

	client := newIntegrationClient(t, redirect, &fakeAuthorizationSource{})
	_, err := client.Search(context.Background(), "tools")
	if !errors.Is(err, ErrRedirect) || targetCalls != 0 {
		t.Fatalf("Search() error=%v target calls=%d", err, targetCalls)
	}
}

func TestClientRejectsResponsesThatReflectItsBearerCredential(t *testing.T) {
	t.Parallel()

	for _, success := range []bool{false, true} {
		success := success
		t.Run(fmt.Sprintf("success_%t", success), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				if success {
					document := completeDocument(serverURL(request), "tools", "search_secret", "", "")
					document["search"].(map[string]any)["query"] =
						request.Header.Get("Authorization")
					writeDocument(response, document)
					return
				}
				response.Header().Set("Content-Type", "application/problem+json")
				response.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(response).Encode(map[string]any{
					"error": map[string]any{
						"code":      "reflected",
						"message":   request.Header.Get("Authorization"),
						"retryable": false,
					},
				})
			}))
			defer server.Close()

			client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
			_, err := client.Search(context.Background(), "tools")
			if !errors.Is(err, ErrProtocol) ||
				strings.Contains(fmt.Sprintf("%v %#v", err, err), "token-one") {
				t.Fatalf("Search() error = %T %v", err, err)
			}
		})
	}
}

func TestClientRejectsUnsupportedSearchDocumentMajorClearly(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		document := completeDocument(
			serverURL(request),
			"tools",
			"search_unsupported",
			"",
			"",
		)
		document["schema_version"] = "kado.search-document.v9"
		document["search"].(map[string]any)["query"] = "must-not-appear"
		writeDocument(response, document)
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	_, err := client.Search(context.Background(), "tools")
	var failure *Error
	if !errors.Is(err, ErrUnsupportedVersion) ||
		!errors.As(err, &failure) ||
		failure.Code() != "search_document_version_unsupported" ||
		!strings.Contains(failure.Error(), "v9") ||
		strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "must-not-appear") {
		t.Fatalf("Search() error = %T %v", err, err)
	}
}

func TestClientRejectsEscapedCredentialReflectionAcrossResponsePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      int
		contentType string
		body        func(string, *http.Request) string
	}{
		{
			name:        "terminal escaped token",
			status:      http.StatusBadRequest,
			contentType: "application/problem+json",
			body: func(_ string, _ *http.Request) string {
				return `{"error":{"code":"reflected","message":"` +
					jsonUnicodeEscapes("token-one") +
					`","retryable":false}}`
			},
		},
		{
			name:        "initial unauthorized bearer plus escaped token",
			status:      http.StatusUnauthorized,
			contentType: "application/json",
			body: func(_ string, _ *http.Request) string {
				return `{"error":{"code":"authentication_required","message":"safe","retryable":false},` +
					`"details":{"nested":["Bearer ` +
					jsonUnicodeEscapes("token-one") +
					`"]}}`
			},
		},
		{
			name:        "successful document nested escaped authorization",
			status:      http.StatusOK,
			contentType: CanonicalMediaType,
			body: func(authorization string, request *http.Request) string {
				document := completeDocument(
					serverURL(request),
					"tools",
					"search_escaped_secret",
					"",
					"",
				)
				document["search"].(map[string]any)["query"] = "__AUTHORIZATION__"
				encoded, err := json.Marshal(document)
				if err != nil {
					t.Fatalf("json.Marshal(document) error = %v", err)
				}
				return strings.Replace(
					string(encoded),
					`"__AUTHORIZATION__"`,
					`"`+jsonUnicodeEscapes(authorization)+`"`,
					1,
				)
			},
		},
	}
	for _, status := range []int{
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		status := status
		tests = append(tests, struct {
			name        string
			status      int
			contentType string
			body        func(string, *http.Request) string
		}{
			name:        "transient " + http.StatusText(status),
			status:      status,
			contentType: "application/json",
			body: func(authorization string, _ *http.Request) string {
				return `{"details":{"diagnostics":[{"reflected":"` +
					jsonUnicodeEscapes(authorization) +
					`"}]}}`
			},
		})
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			source := &fakeAuthorizationSource{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				requests.Add(1)
				authorization := request.Header.Get("Authorization")
				response.Header().Set("Content-Type", test.contentType)
				response.WriteHeader(test.status)
				_, _ = response.Write([]byte(test.body(authorization, request)))
			}))
			defer server.Close()

			client := newIntegrationClient(t, server, source)
			client.wait = noWait
			_, err := client.Search(context.Background(), "tools")
			formatted := fmt.Sprintf("%v %+v %#v", err, err, err)
			if !errors.Is(err, ErrProtocol) ||
				requests.Load() != 1 ||
				source.refreshes != 0 ||
				strings.Contains(formatted, "token-one") ||
				strings.Contains(formatted, "Bearer ") {
				t.Fatalf(
					"Search() error=%T %v requests=%d refreshes=%d",
					err,
					err,
					requests.Load(),
					source.refreshes,
				)
			}
		})
	}
}

func TestClientRejectsSurrogateEscapedCredentialReflection(t *testing.T) {
	t.Parallel()

	authorization := "Bearer token-\U0001f680"
	source := &fixedAuthorizationSource{authorization: authorization}
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		response.Header().Set("Content-Type", "application/problem+json")
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(
			`{"error":{"code":"reflected","message":"` +
				jsonUnicodeEscapes(request.Header.Get("Authorization")) +
				`","retryable":false}}`,
		))
	}))
	defer server.Close()

	client := newIntegrationClient(t, server, source)
	_, err := client.Search(context.Background(), "tools")
	formatted := fmt.Sprintf("%v %+v %#v", err, err, err)
	if !errors.Is(err, ErrProtocol) ||
		source.calls != 1 ||
		strings.Contains(formatted, "token-") ||
		strings.Contains(formatted, "\U0001f680") {
		t.Fatalf("Search() error=%T %v calls=%d", err, err, source.calls)
	}
}

func TestClientRejectsMalformedJSONBeforeRefreshOrRetry(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"truncated object":        `{"details":"safe"`,
		"trailing JSON value":     `{"details":"safe"}{}`,
		"unpaired high surrogate": `{"details":"\ud800"}`,
		"unpaired low surrogate":  `{"details":"\udc00"}`,
		"invalid Unicode escape":  `{"details":"\u00zz"}`,
	} {
		name := name
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, status := range []int{
				http.StatusOK,
				http.StatusBadRequest,
				http.StatusUnauthorized,
				http.StatusBadGateway,
				http.StatusServiceUnavailable,
				http.StatusGatewayTimeout,
			} {
				status := status
				t.Run(http.StatusText(status), func(t *testing.T) {
					t.Parallel()
					var requests atomic.Int32
					source := &fakeAuthorizationSource{}
					server := httptest.NewTLSServer(http.HandlerFunc(func(
						response http.ResponseWriter,
						_ *http.Request,
					) {
						requests.Add(1)
						contentType := "application/json"
						if status == http.StatusOK {
							contentType = CanonicalMediaType
						}
						response.Header().Set("Content-Type", contentType)
						response.WriteHeader(status)
						_, _ = response.Write([]byte(body))
					}))
					defer server.Close()

					client := newIntegrationClient(t, server, source)
					client.wait = noWait
					_, err := client.Search(context.Background(), "tools")
					if !errors.Is(err, ErrProtocol) ||
						requests.Load() != 1 ||
						source.refreshes != 0 {
						t.Fatalf(
							"Search() error=%T %v requests=%d refreshes=%d",
							err,
							err,
							requests.Load(),
							source.refreshes,
						)
					}
				})
			}
		})
	}
}

func TestCredentialReflectionScanPreservesErrorBodyLimit(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		requests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write([]byte(
			`{"details":"` + strings.Repeat("x", 1_024) + `"}`,
		))
	}))
	defer server.Close()

	limits := DefaultLimits()
	limits.MaxErrorBytes = 1_024
	client := newIntegrationClientWithLimits(
		t,
		server,
		&fakeAuthorizationSource{},
		limits,
	)
	client.wait = noWait
	_, err := client.Search(context.Background(), "tools")
	if !errors.Is(err, ErrProtocol) || requests.Load() != 1 {
		t.Fatalf("Search() error=%T %v requests=%d", err, err, requests.Load())
	}
}

func TestRunBoundsLifecycleWorkWithoutALocalTimeout(t *testing.T) {
	t.Parallel()

	t.Run("polling", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			requests.Add(1)
			query := request.URL.Query().Get("q")
			self := serverURL(request) + "/search?q=" + url.QueryEscape(query)
			status := StatusQueued
			if request.URL.Query().Get("operation") == "status" {
				status = StatusRunning
			}
			writeDocument(
				response,
				lifecycleDocument(self, query, "search_bounded_poll", status),
			)
		}))
		defer server.Close()

		limits := DefaultLimits()
		limits.MaxLifecycleOperations = 3
		client := newIntegrationClientWithLimits(
			t,
			server,
			&fakeAuthorizationSource{},
			limits,
		)
		client.wait = noWait
		options := DefaultRunOptions()
		options.Timeout = 0
		options.FollowPages = false
		_, err := client.Run(context.Background(), "bounded poll", options)
		var failure *Error
		if !errors.As(err, &failure) ||
			failure.Code() != "search_lifecycle_limit" ||
			requests.Load() != 3 {
			t.Fatalf("Run() error=%T %v requests=%d", err, err, requests.Load())
		}
	})

	t.Run("clarification", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		var clarifications atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			requests.Add(1)
			if err := request.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			query := request.URL.Query().Get("q")
			if request.Method == http.MethodPost {
				query = request.Form.Get("q")
				if request.Form.Get("operation") != "clarify" {
					t.Fatalf("operation = %q", request.Form.Get("operation"))
				}
			}
			self := serverURL(request) + "/search?q=" + url.QueryEscape(query)
			writeDocument(
				response,
				needsInputDocument(self, query, "search_bounded_clarification"),
			)
		}))
		defer server.Close()

		limits := DefaultLimits()
		limits.MaxLifecycleOperations = 10
		limits.MaxClarifications = 2
		client := newIntegrationClientWithLimits(
			t,
			server,
			&fakeAuthorizationSource{},
			limits,
		)
		options := DefaultRunOptions()
		options.Timeout = 0
		options.FollowPages = false
		options.Clarify = func(context.Context, Question) (string, error) {
			clarifications.Add(1)
			return "Web", nil
		}
		_, err := client.Run(context.Background(), "bounded clarification", options)
		var failure *Error
		if !errors.As(err, &failure) ||
			failure.Code() != "search_clarification_limit" ||
			requests.Load() != 3 ||
			clarifications.Load() != 2 {
			t.Fatalf(
				"Run() error=%T %v requests=%d clarifications=%d",
				err,
				err,
				requests.Load(),
				clarifications.Load(),
			)
		}
	})
}

func TestClientBindsDocumentsAndRelationsToTheExactRequestedQuery(t *testing.T) {
	t.Parallel()

	t.Run("initial_document", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			requests.Add(1)
			writeDocument(
				response,
				completeDocument(serverURL(request), "different query", "search_wrong", "", ""),
			)
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		_, err := client.Search(context.Background(), "requested query")
		if !errors.Is(err, ErrProtocol) || requests.Load() != 1 {
			t.Fatalf("Search() error=%v requests=%d", err, requests.Load())
		}
	})

	t.Run("pagination_relation", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			requests.Add(1)
			next := serverURL(request) + "/search?q=different+query&cursor=opaque_next"
			writeDocument(
				response,
				completeDocument(
					serverURL(request),
					"requested query",
					"search_wrong_relation",
					next,
					"",
				),
			)
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		_, err := client.Search(context.Background(), "requested query")
		if !errors.Is(err, ErrProtocol) || requests.Load() != 1 {
			t.Fatalf("Search() error=%v requests=%d", err, requests.Load())
		}
	})

	t.Run("pagination_self_relation", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			requests.Add(1)
			document := completeDocument(
				serverURL(request),
				"requested query",
				"search_wrong_self_relation",
				"",
				"",
			)
			document["result_set"].(map[string]any)["links"].(map[string]any)["self"] =
				serverURL(request) + "/search?q=different+query"
			writeDocument(response, document)
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		_, err := client.Search(context.Background(), "requested query")
		if !errors.Is(err, ErrProtocol) || requests.Load() != 1 {
			t.Fatalf("Search() error=%v requests=%d", err, requests.Load())
		}
	})

	t.Run("forged_same_origin_relation", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			http.ResponseWriter,
			*http.Request,
		) {
			requests.Add(1)
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		self, _ := url.Parse("https://kado.so/search?q=requested+query")
		next, _ := url.Parse("https://kado.so/search?q=different+query&cursor=opaque_next")
		document := Document{
			self:     self,
			next:     next,
			SearchID: "search_forged_relation",
			Query:    "requested query",
			Status:   StatusComplete,
		}
		_, err := client.Next(context.Background(), document)
		if !errors.Is(err, ErrProtocol) || requests.Load() != 0 {
			t.Fatalf("Next() error=%v requests=%d", err, requests.Load())
		}
	})
}

func TestRunCancelsWithoutHangingWhenClarificationIsInterrupted(t *testing.T) {
	t.Parallel()

	t.Run("parent_interrupt_during_clarifier", func(t *testing.T) {
		t.Parallel()
		var cancels atomic.Int32
		clarifierStarted := make(chan struct{})
		releaseClarifier := make(chan struct{})
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			if request.Method == http.MethodGet {
				query := request.URL.Query().Get("q")
				self := serverURL(request) + "/search?q=" + url.QueryEscape(query)
				writeDocument(
					response,
					needsInputDocument(self, query, "search_interrupt_clarifier"),
				)
				return
			}
			if err := request.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if request.Form.Get("operation") != "cancel" {
				t.Fatalf("operation = %q", request.Form.Get("operation"))
			}
			cancels.Add(1)
			query := request.Form.Get("q")
			self := serverURL(request) + "/search?q=" + url.QueryEscape(query)
			writeDocument(
				response,
				lifecycleDocument(self, query, "search_interrupt_clarifier", StatusCanceled),
			)
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		options := DefaultRunOptions()
		options.Timeout = 0
		options.FollowPages = false
		options.Clarify = func(context.Context, Question) (string, error) {
			close(clarifierStarted)
			<-releaseClarifier
			return "Web", nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		completed := make(chan error, 1)
		go func() {
			_, err := client.Run(ctx, "interrupt clarifier", options)
			completed <- err
		}()
		<-clarifierStarted
		cancel()
		select {
		case err := <-completed:
			close(releaseClarifier)
			if !errors.Is(err, context.Canceled) || cancels.Load() != 1 {
				t.Fatalf("Run() error=%v cancels=%d", err, cancels.Load())
			}
		case <-time.After(2 * time.Second):
			close(releaseClarifier)
			t.Fatal("Run() hung after its clarifier context was canceled")
		}
	})

	t.Run("deadline_during_clarify_request", func(t *testing.T) {
		t.Parallel()
		var cancels atomic.Int32
		clarifyStarted := make(chan struct{})
		var started sync.Once
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			if request.Method == http.MethodGet {
				query := request.URL.Query().Get("q")
				self := serverURL(request) + "/search?q=" + url.QueryEscape(query)
				writeDocument(
					response,
					needsInputDocument(self, query, "search_timeout_clarify"),
				)
				return
			}
			if err := request.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			query := request.Form.Get("q")
			self := serverURL(request) + "/search?q=" + url.QueryEscape(query)
			switch request.Form.Get("operation") {
			case "clarify":
				started.Do(func() { close(clarifyStarted) })
				<-request.Context().Done()
			case "cancel":
				cancels.Add(1)
				writeDocument(
					response,
					lifecycleDocument(self, query, "search_timeout_clarify", StatusCanceled),
				)
			default:
				t.Fatalf("operation = %q", request.Form.Get("operation"))
			}
		}))
		defer server.Close()

		client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
		options := DefaultRunOptions()
		options.Timeout = 100 * time.Millisecond
		options.FollowPages = false
		options.Clarify = func(context.Context, Question) (string, error) {
			return "Web", nil
		}
		completed := make(chan error, 1)
		go func() {
			_, err := client.Run(context.Background(), "timeout clarify", options)
			completed <- err
		}()
		<-clarifyStarted
		select {
		case err := <-completed:
			if !errors.Is(err, ErrTimeout) || cancels.Load() != 1 {
				t.Fatalf("Run() error=%v cancels=%d", err, cancels.Load())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Run() hung after its clarify request deadline elapsed")
		}
	})
}

func TestClientRejectsCredentialReflectionBeforeRefreshOrRetry(t *testing.T) {
	t.Parallel()

	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			source := &fakeAuthorizationSource{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				requests.Add(1)
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(status)
				_, _ = response.Write([]byte(
					`{"error":{"code":"reflected","message":"` +
						request.Header.Get("Authorization") +
						`","retryable":true}}`,
				))
			}))
			defer server.Close()

			client := newIntegrationClient(t, server, source)
			client.wait = noWait
			_, err := client.Search(context.Background(), "tools")
			if !errors.Is(err, ErrProtocol) ||
				requests.Load() != 1 ||
				source.refreshes != 0 ||
				strings.Contains(fmt.Sprintf("%v %#v", err, err), "token-one") {
				t.Fatalf(
					"Search() error=%T %v requests=%d refreshes=%d",
					err,
					err,
					requests.Load(),
					source.refreshes,
				)
			}
		})
	}
}

func TestNewRejectsAmbiguousOrEscapedBaseURLs(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"http://kado.so",
		"https://user@kado.so",
		"https://kado.so//api",
		"https://kado.so/api/",
		"https://kado.so/api/../v1",
		"https://kado.so/api%2fv1",
		"https://kado.so/api:v1",
	}
	for _, input := range inputs {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			base, err := url.Parse(input)
			if err != nil {
				t.Fatalf("url.Parse(%q) error = %v", input, err)
			}
			_, err = New(base, http.DefaultClient, &fakeAuthorizationSource{}, DefaultLimits())
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("New(%q) error = %v, want ErrProtocol", input, err)
			}
		})
	}
}

func TestNewRejectsUnsafeLifecycleLimits(t *testing.T) {
	t.Parallel()

	for name, modify := range map[string]func(*Limits){
		"zero_operations": func(limits *Limits) {
			limits.MaxLifecycleOperations = 0
		},
		"excessive_operations": func(limits *Limits) {
			limits.MaxLifecycleOperations = 10_001
		},
		"zero_clarifications": func(limits *Limits) {
			limits.MaxClarifications = 0
		},
		"excessive_clarifications": func(limits *Limits) {
			limits.MaxClarifications = 65
		},
	} {
		name := name
		modify := modify
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			limits := DefaultLimits()
			modify(&limits)
			base, err := url.Parse("https://kado.so")
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			_, err = New(
				base,
				http.DefaultClient,
				&fakeAuthorizationSource{},
				limits,
			)
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("New() error = %v, want ErrProtocol", err)
			}
		})
	}
}

type fakeAuthorizationSource struct {
	calls     int
	refreshes int
}

func (source *fakeAuthorizationSource) Authorization(
	_ context.Context,
	refresh bool,
) (string, error) {
	source.calls++
	if refresh {
		source.refreshes++
		return "Bearer token-two", nil
	}
	if source.refreshes > 0 {
		return "Bearer token-two", nil
	}
	return "Bearer token-one", nil
}

type fixedAuthorizationSource struct {
	authorization string
	calls         int
}

func (source *fixedAuthorizationSource) Authorization(
	_ context.Context,
	_ bool,
) (string, error) {
	source.calls++
	return source.authorization, nil
}

func jsonUnicodeEscapes(value string) string {
	var output strings.Builder
	for _, character := range value {
		if character <= 0xffff {
			_, _ = fmt.Fprintf(&output, `\u%04x`, character)
			continue
		}
		high, low := utf16.EncodeRune(character)
		_, _ = fmt.Fprintf(&output, `\u%04x\u%04x`, high, low)
	}
	return output.String()
}

func newIntegrationClient(
	t *testing.T,
	server *httptest.Server,
	source AuthorizationSource,
) *Client {
	t.Helper()
	return newIntegrationClientWithLimits(t, server, source, DefaultLimits())
}

func newIntegrationClientWithLimits(
	t *testing.T,
	server *httptest.Server,
	source AuthorizationSource,
	limits Limits,
) *Client {
	t.Helper()
	base, err := url.Parse("https://kado.so")
	if err != nil {
		t.Fatalf("Parse(kado.so) error = %v", err)
	}
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse(server URL) error = %v", err)
	}
	httpClient := *server.Client()
	httpClient.Transport = &testServerTransport{
		target: target,
		next:   httpClient.Transport,
	}
	client, err := New(base, &httpClient, source, limits)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

type testServerTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (transport *testServerTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	endpoint := *request.URL
	endpoint.Scheme = transport.target.Scheme
	endpoint.Host = transport.target.Host
	cloned.URL = &endpoint
	cloned.Host = "kado.so"
	return transport.next.RoundTrip(cloned)
}

func assertMachineRequest(t *testing.T, request *http.Request, authorization string) {
	t.Helper()
	if request.Header.Get("Accept") != CanonicalMediaType {
		t.Fatalf("Accept = %q", request.Header.Get("Accept"))
	}
	if request.Header.Get("Authorization") != authorization {
		t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
	}
}

func writeDocument(response http.ResponseWriter, document map[string]any) {
	response.Header().Set("Content-Type", CanonicalMediaType+"; charset=utf-8")
	_ = json.NewEncoder(response).Encode(document)
}

func writeProblem(
	response http.ResponseWriter,
	status int,
	code string,
	retryable bool,
) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   "Authentication is required.",
			"retryable": retryable,
		},
	})
}

func lifecycleDocument(
	self string,
	query string,
	searchID string,
	status string,
) map[string]any {
	state := map[string]any{"status": status}
	if status == StatusRunning {
		state["progress"] = map[string]any{
			"percent": 40,
			"message": "Searching.",
		}
	}
	if status == StatusCanceled {
		state["reason"] = "Canceled by the requester."
	}
	return baseDocument(self, query, searchID, state)
}

func needsInputDocument(self, query, searchID string) map[string]any {
	return baseDocument(self, query, searchID, map[string]any{
		"status": StatusNeedsInput,
		"question": map[string]any{
			"id":      "question_platform",
			"prompt":  "Which platform?",
			"options": []string{"Web", "Desktop", "Mobile"},
		},
	})
}

func failedDocument(
	self string,
	query string,
	searchID string,
	code string,
	message string,
	retryable bool,
) map[string]any {
	return baseDocument(self, query, searchID, map[string]any{
		"status": StatusFailed,
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryable,
		},
	})
}

func unsafeDiagnosticRune(character rune) bool {
	return unicode.IsControl(character) ||
		unicode.In(character, unicode.Cf, unicode.Zl, unicode.Zp)
}

func completeDocument(
	origin string,
	query string,
	searchID string,
	next string,
	previous string,
) map[string]any {
	self := origin + "/search?q=" + url.QueryEscape(query)
	document := baseDocument(self, query, searchID, map[string]any{
		"status": StatusComplete,
	})
	var nextValue any
	if next != "" {
		nextValue = next
	}
	var previousValue any
	if previous != "" {
		previousValue = previous
	}
	document["result_set"] = map[string]any{
		"@type":       "ItemList",
		"result_type": "mixed_results",
		"items":       []any{},
		"pagination": map[string]any{
			"kind":            "cursor",
			"page_size":       20,
			"returned":        0,
			"total":           nil,
			"has_more":        next != "",
			"next_cursor":     cursorFromLink(next),
			"previous_cursor": cursorFromLink(previous),
		},
		"links": map[string]any{
			"self":     self,
			"next":     nextValue,
			"previous": previousValue,
		},
	}
	return document
}

func baseDocument(
	self string,
	query string,
	searchID string,
	state map[string]any,
) map[string]any {
	search := map[string]any{
		"id":         searchID,
		"query":      query,
		"created_at": "2026-07-23T00:00:00Z",
	}
	if state["status"] != StatusQueued {
		search["started_at"] = "2026-07-23T00:00:01Z"
	}
	switch state["status"] {
	case StatusComplete, StatusFailed, StatusCanceled:
		search["completed_at"] = "2026-07-23T00:00:02Z"
	}
	return map[string]any{
		"@context":       "https://kado.so/contexts/search-document/v1.jsonld",
		"@id":            self,
		"@type":          "SearchResultsPage",
		"schema_version": SchemaVersion,
		"search":         search,
		"state":          state,
		"links": map[string]any{
			"self":    self,
			"schema":  "https://kado.so/schemas/search-document/v1.json",
			"context": "https://kado.so/contexts/search-document/v1.jsonld",
		},
		"metadata": map[string]any{
			"revision":     1,
			"generated_at": "2026-07-23T00:00:02Z",
		},
	}
}

func serverURL(request *http.Request) string {
	return "https://" + request.Host
}

func cursorFromLink(raw string) any {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	cursor := parsed.Query().Get("cursor")
	if cursor == "" {
		return "opaque_relation"
	}
	return cursor
}

func noWait(context.Context, time.Duration) error {
	return nil
}
