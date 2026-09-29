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
	concurrency := flag.Int("concurrency", 8, "Fetches in flight at once")
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

	svc := garmtool.New("web", version(), garmtool.WithConcurrency(*concurrency))
	fetcher := web.NewFetcher(policy, web.WithLogger(log))
	if err := webv1.ServeWebService(svc, web.NewService(fetcher)); err != nil {
		return fmt.Errorf("registering web: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("serving", "tool", "web.v1.fetch_page", "version", version(), "nats", nc.ConnectedUrl())
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
