// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// Command kubectl-probes is a read-only kubectl plugin that shows, per workload
// and container, which startup, readiness, and liveness probes are configured,
// what their settings mean in practice, and what evidence of failure the
// cluster currently reports.
package main

import (
	goflag "flag"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"

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
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
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
  kubectl probes -f deploy.yaml -o json`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(args)
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
	flags.StringVarP(&o.selector, "selector", "l", "",
		"Label selector to filter workloads by")
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

func (o *options) run(args []string) error {
	if err := o.validate(); err != nil {
		return err
	}
	setupColor(o.color)

	// Collecting, analyzing, and rendering land here. The internal packages
	// are deliberately empty until those tickets fill them in.
	_ = args
	return nil
}

func (o *options) validate() error {
	if err := oneOf("--output", o.output, outputFormats); err != nil {
		return err
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
