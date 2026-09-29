// Command searchd serves search_web over NATS.
//
// It refuses to start without a policy file and an API key file, and
// refuses to start on a policy it cannot read, that names no allowlist, or
// whose endpoint could never be reached. A search service that came up on a
// bad policy would either return nothing and look like an outage or return
// links nobody vetted and look like a feature; refusing at boot is the only
// answer an operator can act on.
//
// Nothing here authenticates, authorises, checks input or redacts a
// response. The daemon did all of that before the request arrived.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/garm-ai/tool-go/garmtool"
	"github.com/garm-ai/tools/search"
	searchv1 "github.com/garm-ai/tools/search/gen/search/v1"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("searchd stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	url := flag.String("nats", nats.DefaultURL, "NATS server URL")
	policyPath := flag.String("policy", "", "Path to the endpoint and allow/block policy (required)")
	// A PATH, never a value. A --api-key flag would put the credential in
	// /proc/<pid>/cmdline and in every `ps` on the host; an SEARCHD_API_KEY
	// holding the key itself would be inherited by every child and dumped
	// by anything that writes a crash report. SEARCHD_API_KEY_FILE holds a
	// path too. This follows agentd's --provider-key-file, which is the
	// strictest pattern in this platform.
	keyPath := flag.String("api-key-file", os.Getenv("SEARCHD_API_KEY_FILE"),
		"Path to a file holding the search provider's API key and nothing else (required; SEARCHD_API_KEY_FILE)")
	// Four, which is tool-go's DefaultConcurrency, chosen rather than
	// inherited.
	//
	// Since tool-go v0.6.0 a handler runs synchronously in the goroutine its
	// subscription owns, so concurrency is subscriptions: WithConcurrency(n)
	// registers n whole micro service instances in one queue group, each a
	// separate responder on $SRV.INFO, $SRV.PING and $SRV.STATS. garmd's
	// discovery collects INFO replies into a channel buffered at 64 and
	// drops silently past that, so the budget is shared with every other
	// service in the plane. That is the cost side, and it is the same cost
	// webd pays.
	//
	// webd went to 8 because a page fetch is a long wait: a TLS handshake, up
	// to five redirects, up to five megabytes of body, a twenty-second cap. A
	// search is one short JSON round trip to one endpoint with no redirects
	// and a ten-second cap, so the queue drains several times faster per
	// instance and four in-flight searches is not the same bottleneck four
	// in-flight page loads was.
	//
	// The real ceiling is the vendor's, not this one's: a Brave subscription
	// is rated in requests per second, and the entry plans are at or below
	// what four instances can drive. Buying responders that the provider
	// would rate-limit anyway spends a budget garmd shares across the plane
	// to produce 429s. Past this, throughput is a deployment question — a
	// larger subscription first, then more searchd processes behind the same
	// queue group.
	concurrency := flag.Int("concurrency", 4,
		"Micro service instances, each handling one search at a time (every instance is a separate $SRV.INFO responder)")
	flag.Parse()

	if *concurrency <= 0 {
		return errors.New("--concurrency must be at least 1")
	}
	if *policyPath == "" {
		return errors.New("--policy is required: this service returns nothing without an allowlist")
	}
	if *keyPath == "" {
		return errors.New("--api-key-file is required (or SEARCHD_API_KEY_FILE): the search provider needs a credential, and it is taken as a file so it is never in a process list")
	}
	policy, err := search.LoadPolicy(*policyPath)
	if err != nil {
		return err
	}
	apiKey, err := search.LoadAPIKey(*keyPath)
	if err != nil {
		return err
	}

	// Everything the operator configured, and not one character of the key.
	// The path is named because the path is what the operator typed; the
	// value is named nowhere, in this line or any other.
	log.Info("policy loaded", "path", *policyPath, "digest", policy.Digest(),
		"backend", policy.Backend, "endpoint", policy.EndpointURL().String(),
		"allow", len(policy.Allow), "block", len(policy.Block),
		"max_results", policy.MaxResults, "max_snippet_chars", policy.MaxSnippetChars,
		"timeout", policy.Timeout, "user_agent", policy.UserAgent,
		"api_key_file", *keyPath)
	if w := search.KeyFileWarning(*keyPath); w != "" {
		log.Warn(w, "api_key_file", *keyPath)
	}

	// Reconnect forever rather than exit. A tool service that dies because
	// NATS blinked turns a transient outage into a deployment event.
	nc, err := nats.Connect(*url,
		nats.Name("searchd"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			// A nil error is our own Close on the way out, not a disconnect.
			if err != nil {
				log.Warn("disconnected from nats", "err", err)
			}
		}),
	)
	if err != nil {
		return fmt.Errorf("connecting to nats at %s: %w", *url, err)
	}
	defer nc.Close()

	// The same logger the policy and the searcher write to. WithLogger is
	// what makes the runtime say what is actually in force — concurrency,
	// name, version, queue groups, the descriptor identity and the contract
	// version — with a default reported exactly like a passed value.
	// Without it the library is silent by design, and an operator reading
	// this service's log would have to read tool-go's source to learn which
	// numbers are running.
	svc := garmtool.New("search", version(),
		garmtool.WithConcurrency(*concurrency),
		garmtool.WithLogger(log),
	)
	searcher := search.NewSearcher(policy, apiKey, search.WithLogger(log))
	if err := searchv1.ServeSearchService(svc, search.NewService(searcher)); err != nil {
		return fmt.Errorf("registering search: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Only what the runtime cannot know: which server this process reached.
	// garmtool writes the rest — service, version, tool count, concurrency,
	// queue groups, identity, contract version — once the subscriptions are
	// up, so repeating any of it here would be two lines that can disagree.
	log.Info("connected", "tool", "search.v1.search_web", "nats", nc.ConnectedUrl())
	if err := svc.Run(ctx, nc); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("drained")
	return nil
}

// version is the module version the toolchain stamped, which the daemon
// reads back from $SRV.INFO: v0.1.0 when installed with
// `go install …/cmd/searchd@v0.1.0`. Any in-tree build — tagged checkout or
// not — is stamped "(devel)", because Go stamps only a root module's tag and
// search is a nested module; "(devel)" is not a version NATS micro accepts,
// so it is reported as a dev build.
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "0.0.0-dev"
}
