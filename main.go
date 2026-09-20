// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// Command kubectl-probes is a read-only kubectl plugin that shows, per workload
// and container, which startup, readiness, and liveness probes are configured,
// what their settings mean in practice, and what evidence of failure the
// cluster currently reports.
package main

import (
	"context"
	"errors"
	goflag "flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"

	"github.com/mertdotcc/kubectl-probes/internal/analyze"
	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
	"github.com/mertdotcc/kubectl-probes/internal/render"
	"github.com/mertdotcc/kubectl-probes/internal/serve"

	// Authentication plugins so kubeconfigs pointing at GKE, EKS, AKS, and
	// OIDC providers work the same way they do for kubectl itself.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// version is set by goreleaser through -ldflags at release time. When it is
// empty cobra adds no --version flag, so development builds do not advertise
// a version they do not have.
var version string

// Values accepted by -o/--output.
const (
	outputTable = "table"
	outputWide  = "wide"
	outputJSON  = "json"
	outputYAML  = "yaml"
)

// Values accepted by -c/--color.
const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorNever  = "never"
)

// Values accepted by --sort.
const (
	sortName     = "name"
	sortSeverity = "severity"
)

// serveDefaultAddr is where --serve binds when it is given no address: a free
// port on loopback, because the Dashboard never authenticates. See
// docs/adr/0006-the-dashboard-ships-inside-the-plugin-binary.md.
const serveDefaultAddr = "127.0.0.1:0"

var (
	outputFormats = []string{outputTable, outputWide, outputJSON, outputYAML}
	colorModes    = []string{colorAuto, colorAlways, colorNever}
	sortOrders    = []string{sortName, sortSeverity}
)

// options is everything the root command accepts, in one place, so the
// collect, analyze, and render packages can be handed exactly what they need.
type options struct {
	configFlags *genericclioptions.ConfigFlags

	allNamespaces bool
	selector      string
	filenames     []string
	output        string
	color         string
	sort          string
	noFindings    bool
	serve         string
}

func main() {
	// --serve runs until it is interrupted, so the command is given a context
	// a signal can cancel. Every other run finishes long before it matters.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintf(color.Error, "error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	o := &options{configFlags: genericclioptions.NewConfigFlags(true)}

	cmd := &cobra.Command{
		Use:   "kubectl probes [TYPE[/NAME]] [flags]",
		Short: "Show probe configuration, what it means, and how it is failing",
		Long: `Show, per workload and container, which startup, readiness, and liveness
probes are configured, what their settings mean in practice, and what
evidence of failure the cluster currently reports.

The plugin only reads from the API server. It never exercises a probe.`,
		Example: `  # Every workload in the current namespace
  kubectl probes

  # One workload, in detail
  kubectl probes deploy/api

  # Every namespace, widest table, no opinions
  kubectl probes -A -o wide --no-findings

  # A manifest that has not been applied yet
  kubectl probes -f deploy.yaml -o json

  # The Dashboard, in a browser, opened on one workload
  kubectl probes deploy/api --serve`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(cmd, args)
		},
	}

	// Only exit codes 0 and 1 are ever used, so every diagnostic goes through
	// the single error path in main rather than calling os.Exit here.
	cmd.SetOut(color.Output)
	cmd.SetErr(color.Error)

	if version != "" {
		cmd.Version = version
		cmd.SetVersionTemplate("{{.Version}}\n")
	}

	flags := cmd.Flags()
	o.configFlags.AddFlags(flags)
	flags.BoolVarP(&o.allNamespaces, "all-namespaces", "A", false,
		"Show workloads in every namespace")
	// The selector reaches the pod list, so it is pod labels it is matched
	// against and not the labels on the Deployment above them. Saying so here
	// is the difference between finding nothing and knowing why.
	flags.StringVarP(&o.selector, "selector", "l", "",
		"Label selector, matched against pod labels")
	flags.StringSliceVarP(&o.filenames, "filename", "f", nil,
		"Read workloads from a manifest instead of the cluster")
	flags.StringVarP(&o.output, "output", "o", outputTable,
		fmt.Sprintf("Output format, one of %s", strings.Join(outputFormats, "|")))
	flags.StringVarP(&o.color, "color", "c", colorAuto,
		fmt.Sprintf("When to colorize output, one of %s", strings.Join(colorModes, "|")))
	flags.StringVar(&o.sort, "sort", sortName,
		fmt.Sprintf("Order of the Overview rows, one of %s", strings.Join(sortOrders, "|")))
	flags.BoolVar(&o.noFindings, "no-findings", false,
		"Report facts only, without findings")
	// The Dashboard is unauthenticated, so the address is the whole of its
	// security and the help text is where that is said. An optional value
	// keeps the bare --serve meaning the safe thing.
	flags.StringVar(&o.serve, "serve", "",
		"Serve the Dashboard on ADDR, by default a free port on loopback. It is unauthenticated, so an address off loopback hands the cluster's report to the network")
	flags.Lookup("serve").NoOptDefVal = serveDefaultAddr
	addKlogFlags(flags)

	// Cobra names the command after the first word of Use, which is "kubectl"
	// here. Register the flags it would otherwise generate so their help text
	// names the plugin instead.
	flags.BoolP("help", "h", false, "help for kubectl probes")
	if version != "" {
		flags.Bool("version", false, "version for kubectl probes")
	}

	return cmd
}

// run is the whole plugin: read what the flags asked for, interpret it, and
// print it, or hand it to the Dashboard under --serve. Nothing below decides
// anything about the cluster, so the same cluster reports the same twice in a
// row.
func (o *options) run(cmd *cobra.Command, args []string) error {
	if err := o.validate(); err != nil {
		return err
	}
	setupColor(o.color)

	if o.serve != "" {
		return o.runServe(cmd, args)
	}

	result, err := collect.Collect(cmd.Context(), o.collectOptions(args))
	if err != nil {
		return err
	}

	// Every age the Inspection prints is measured against this one moment, so
	// a report does not drift as it is written out.
	report := analyze.Analyze(result, time.Now(), o.analyzeOptions())

	// The writers come off the command rather than from color.Output and
	// color.Error directly. The root command already points them there, and
	// taking them from here is what lets a test drive the command.
	return o.write(cmd.OutOrStdout(), cmd.ErrOrStderr(), report, len(args) > 0)
}

// runServe is --serve: the same report, published to a browser and kept
// current, instead of printed once. It returns when a signal cancels the
// command's context, which is the only way a served run ends.
func (o *options) runServe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	source, err := o.source(ctx, args)
	if err != nil {
		// A signal that arrived while the watch was still filling its caches
		// is how the user asked to stop, not a failure to report.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	// The command takes at most one positional, and it names the workload the
	// Dashboard opens on.
	var initial string
	if len(args) > 0 {
		initial = args[0]
	}

	return serve.Run(ctx, serve.Options{
		Addr:    o.serve,
		Initial: initial,
		Analyze: o.analyzeOptions(),
		Source:  source,
		// The URL goes to stderr for the same reason every other note does:
		// a run whose stdout was redirected still says where to look.
		Out: cmd.ErrOrStderr(),
	})
}

// source is where the Dashboard's Reports come from. A set of -f files is a
// cluster as it was written down, so there is nothing in it to watch: it is
// read once and served as it is. See ADR-0003.
func (o *options) source(ctx context.Context, args []string) (serve.Source, error) {
	if len(o.filenames) > 0 {
		result, err := collect.Collect(ctx, o.collectOptions(args))
		if err != nil {
			return serve.Source{}, err
		}
		return serve.Snapshot(result), nil
	}

	results, err := collect.Watch(ctx, o.collectOptions(args))
	if err != nil {
		return serve.Source{}, err
	}
	return serve.Stream(results), nil
}

// collectOptions is what the flags mean to the collect package, shared by the
// one read a printed run makes and the watch a served one keeps, so the two
// can never disagree about which workloads they are about.
func (o *options) collectOptions(args []string) collect.Options {
	return collect.Options{
		ConfigFlags:   o.configFlags,
		Args:          args,
		Filenames:     o.filenames,
		Selector:      o.selector,
		AllNamespaces: o.allNamespaces,
	}
}

// write puts the report on the surface the flags asked for.
//
// The Inspection is the view of a single workload, so it is what a run that
// named one gets. A run that named nothing, or one whose argument turned out
// to cover more than one workload, gets the Overview: there is no reading of
// the Inspection that covers two workloads at once.
func (o *options) write(out, errOut io.Writer, report *model.Report, named bool) error {
	switch o.output {
	case outputJSON:
		return report.Encode(out, model.FormatJSON)
	case outputYAML:
		return report.Encode(out, model.FormatYAML)
	}
	if named && len(report.Workloads) == 1 {
		return render.Inspection(out, errOut, report)
	}
	return render.Overview(out, errOut, report, o.renderOptions())
}

// renderOptions is what the flags mean to the Overview.
func (o *options) renderOptions() render.Options {
	return render.Options{
		AllNamespaces: o.allNamespaces,
		Wide:          o.output == outputWide,
		Sort:          render.SortOrder(o.sort),
	}
}

// analyzeOptions is what the flags mean to the analyze package.
//
// --no-findings is applied there, at the source, so the Report itself carries
// no opinions and every output format is quiet about them without each having
// to remember to be.
func (o *options) analyzeOptions() analyze.Options {
	return analyze.Options{NoFindings: o.noFindings}
}

func (o *options) validate() error {
	if err := oneOf("--output", o.output, outputFormats); err != nil {
		return err
	}
	// The Dashboard is a surface of its own, and it is the JSON encoding it
	// serves. There is no run that is both a pipe and a browser.
	if o.serve != "" && o.output != outputTable {
		return errors.New("--serve cannot be combined with --output")
	}
	if err := oneOf("--color", o.color, colorModes); err != nil {
		return err
	}
	return oneOf("--sort", o.sort, sortOrders)
}

func oneOf(flag, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("invalid value %q for %s: must be one of %s",
		value, flag, strings.Join(allowed, ", "))
}

// setupColor applies -c/--color. In auto mode fatih/color has already made the
// call at init time from NO_COLOR, TERM, and whether stdout is a terminal, so
// auto is the absence of an override rather than a mode of its own.
func setupColor(mode string) {
	switch mode {
	case colorAlways:
		color.NoColor = false
	case colorNever:
		color.NoColor = true
	}
}

// addKlogFlags exposes klog's flags so client-go logging can be turned up, but
// hides all of them except -v so --help stays about this plugin.
func addKlogFlags(flags *pflag.FlagSet) {
	klogFlags := goflag.NewFlagSet("klog", goflag.ContinueOnError)
	klog.InitFlags(klogFlags)

	wrapped := pflag.NewFlagSet("klog", pflag.ContinueOnError)
	wrapped.AddGoFlagSet(klogFlags)
	wrapped.VisitAll(func(f *pflag.Flag) {
		if f.Name == "v" {
			f.Shorthand = "v"
			return
		}
		f.Hidden = true
	})
	flags.AddFlagSet(wrapped)
}
