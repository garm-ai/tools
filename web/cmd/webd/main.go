// Command webd serves fetch_page over NATS.
//
// It refuses to start without a policy file, and refuses to start on a
// policy it cannot read or that allows no hosts. A fetch service that came
// up on a bad policy would either fetch nothing and look like an outage or
// fetch anything and look like a feature; refusing at boot is the only
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
	"github.com/garm-ai/tools/web"
	webv1 "github.com/garm-ai/tools/web/gen/web/v1"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("webd stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	url := flag.String("nats", nats.DefaultURL, "NATS server URL")
	policyPath := flag.String("policy", "", "Path to the allow/block policy (required)")
	// Eight, and since tool-go v0.6.0 the unit is a whole NATS micro service
	// instance rather than a goroutine. That changed what the number costs.
	//
	// A handler now runs synchronously in the goroutine its subscription owns,
	// so concurrency is subscriptions: n instances, each answering $SRV.INFO,
	// $SRV.PING and $SRV.STATS separately. garmd's discovery collects INFO
	// replies into a channel buffered at 64 and drops silently past that, so
	// the budget is shared with every other service in the plane and a large
	// value here degrades discovery for all of them. That is why the library
	// default is 4 and not 16.
	//
	// Eight is a deliberate step above that default, not a survival of the
	// old one: a fetch spends its whole life waiting on a host someone else
	// runs, so four in-flight page loads is a queue where the CPU is idle.
	// Double the default costs webd four extra responders against a ceiling
	// of 64 — the reference plane's ten or eleven services at the default sit
	// near forty — which fits with room left. Past this, throughput is a
	// deployment question: run more webd processes behind the same queue
	// group. Raising it into the tens is a garmd change first.
	concurrency := flag.Int("concurrency", 8,
		"Micro service instances, each handling one fetch at a time (every instance is a separate $SRV.INFO responder)")
	flag.Parse()

	if *concurrency <= 0 {
		return errors.New("--concurrency must be at least 1")
	}
	if *policyPath == "" {
		return errors.New("--policy is required: this service fetches nothing without an allowlist")
	}
	policy, err := web.LoadPolicy(*policyPath)
	if err != nil {
		return err
	}
	log.Info("policy loaded", "path", *policyPath, "digest", policy.Digest(),
		"allow", len(policy.Allow), "block", len(policy.Block))

	// Reconnect forever rather than exit. A tool service that dies because
	// NATS blinked turns a transient outage into a deployment event.
	nc, err := nats.Connect(*url,
		nats.Name("webd"),
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

	// The same logger the policy and the fetcher write to. WithLogger is what
	// makes the runtime say what is actually in force — concurrency, name,
	// version, queue groups, the descriptor identity and the contract version
	// — with a default reported exactly like a passed value. Without it the
	// library is silent by design, and an operator reading this service's log
	// would have to read tool-go's source to learn which numbers are running.
	svc := garmtool.New("web", version(),
		garmtool.WithConcurrency(*concurrency),
		garmtool.WithLogger(log),
	)
	fetcher := web.NewFetcher(policy, web.WithLogger(log))
	if err := webv1.ServeWebService(svc, web.NewService(fetcher)); err != nil {
		return fmt.Errorf("registering web: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Only what the runtime cannot know: which server this process reached.
	// garmtool writes the rest — service, version, tool count, concurrency,
	// queue groups, identity, contract version — once the subscriptions are
	// up, so repeating any of it here would be two lines that can disagree.
	log.Info("connected", "tool", "web.v1.fetch_page", "nats", nc.ConnectedUrl())
	if err := svc.Run(ctx, nc); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("drained")
	return nil
}

// version is the module version the toolchain stamped, which the daemon
// reads back from $SRV.INFO: v0.1.1 when installed with
// `go install …/cmd/webd@v0.1.1`. Any in-tree build — tagged checkout or
// not — is stamped "(devel)", because Go stamps only a root module's tag and
// web is a nested module; "(devel)" is not a version NATS micro accepts, so
// it is reported as a dev build.
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "0.0.0-dev"
}
