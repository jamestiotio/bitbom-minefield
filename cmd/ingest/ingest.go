package ingest

import (
	"github.com/bitbomdev/minefield/cmd/ingest/osv"
	"github.com/bitbomdev/minefield/cmd/ingest/sbom"
	"github.com/bitbomdev/minefield/cmd/ingest/scorecard"
	"github.com/bitbomdev/minefield/pkg/graph"
	"github.com/spf13/cobra"
)

type options struct{}

func (o *options) AddFlags(_ *cobra.Command) {
}

func New(storage graph.Storage) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "ingest",
		Short:             "ingest metadata into the graph",
		SilenceUsage:      true,
		DisableAutoGenTag: true,
	}

	cmd.AddCommand(osv.New(storage))
	cmd.AddCommand(sbom.New(storage))
	cmd.AddCommand(scorecard.New(storage))
	return cmd
}
