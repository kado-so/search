package searchclient

import (
	"context"
	"errors"
	"time"
)

// Run executes a Search through terminal state and follows opaque next-page
// relations.
func (client *Client) Run(
	ctx context.Context,
	query string,
	options RunOptions,
) (Result, error) {
	if options.Timeout < 0 ||
		options.Timeout > 30*time.Minute ||
		options.PollInterval < 0 ||
		options.PollInterval > time.Minute {
		return Result{}, newError(
			"search_options_invalid",
			"Search lifecycle options are invalid.",
			0,
			false,
			ErrProtocol,
		)
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Second
	}
	runContext := ctx
	cancel := func() {}
	if options.Timeout > 0 {
		runContext, cancel = context.WithTimeout(ctx, options.Timeout)
	}
	defer cancel()

	document, err := client.Search(runContext, query)
	if err != nil {
		return Result{}, timeoutResult(err)
	}
	lifecycleOperations := 1
	retriedFailure := false
	for {
		switch document.Status {
		case StatusComplete:
			pages, err := client.followPages(runContext, document, options.FollowPages)
			if err != nil {
				return Result{Document: document}, timeoutResult(err)
			}
			return Result{Document: document, Pages: pages}, nil
		case StatusQueued, StatusRunning:
			if err := client.wait(runContext, options.PollInterval); err != nil {
				client.cancelAfterInterruption(ctx, document, options.CancelOnTimeout)
				return Result{Document: document}, timeoutResult(err)
			}
			if lifecycleOperations >= client.limits.MaxLifecycleOperations {
				return Result{Document: document}, lifecycleLimitError()
			}
			lifecycleOperations++
			next, err := client.Status(runContext, document)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) ||
					errors.Is(err, context.Canceled) {
					client.cancelAfterInterruption(ctx, document, options.CancelOnTimeout)
				}
				return Result{Document: document}, timeoutResult(err)
			}
			if err := validateTransition(document, next); err != nil {
				return Result{Document: document}, err
			}
			document = next
		case StatusFailed:
			if document.Failure == nil {
				return Result{Document: document}, protocolError()
			}
			if options.RetryFailure && document.Failure.Retryable && !retriedFailure {
				retriedFailure = true
				if lifecycleOperations >= client.limits.MaxLifecycleOperations {
					return Result{Document: document}, lifecycleLimitError()
				}
				lifecycleOperations++
				next, err := client.Retry(runContext, document)
				if err != nil {
					return Result{Document: document}, timeoutResult(err)
				}
				if err := validateTransition(document, next); err != nil {
					return Result{Document: document}, err
				}
				document = next
				continue
			}
			return Result{Document: document}, &FailureError{
				Failure: *document.Failure,
			}
		case StatusCanceled:
			return Result{Document: document}, &CanceledError{}
		default:
			return Result{Document: document}, protocolError()
		}
	}
}

func (client *Client) followPages(
	ctx context.Context,
	first Document,
	enabled bool,
) ([]Document, error) {
	pages := []Document{first}
	if !enabled {
		return pages, nil
	}
	seen := map[string]struct{}{first.self.String(): {}}
	current := first
	for current.next != nil {
		if len(pages) >= client.limits.MaxPages {
			return nil, newError(
				"search_page_limit",
				"Search returned too many pages.",
				0,
				false,
				ErrProtocol,
			)
		}
		nextURL := current.next.String()
		if _, duplicate := seen[nextURL]; duplicate {
			return nil, protocolError()
		}
		seen[nextURL] = struct{}{}
		next, err := client.Next(ctx, current)
		if err != nil {
			return nil, err
		}
		if err := validateTransition(first, next); err != nil ||
			next.Status != StatusComplete {
			return nil, protocolError()
		}
		pages = append(pages, next)
		current = next
	}
	return pages, nil
}

func (client *Client) cancelAfterInterruption(
	parent context.Context,
	document Document,
	enabled bool,
) {
	if !enabled || document.Status == StatusComplete ||
		document.Status == StatusFailed ||
		document.Status == StatusCanceled {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	_, _ = client.Cancel(cleanup, document)
}

func validateTransition(previous, next Document) error {
	if previous.SearchID != next.SearchID || previous.Query != next.Query {
		return protocolError()
	}
	return nil
}

func timeoutResult(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{}
	}
	return err
}

func lifecycleLimitError() error {
	return newError(
		"search_lifecycle_limit",
		"Search exceeded the local lifecycle operation limit.",
		0,
		false,
		ErrProtocol,
	)
}
