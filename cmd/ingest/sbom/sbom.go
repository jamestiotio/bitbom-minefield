package sbom

import (
	"fmt"

	"github.com/bitbomdev/minefield/pkg/graph"
	"github.com/bitbomdev/minefield/pkg/tools"
	"github.com/bitbomdev/minefield/pkg/tools/ingest"
	"github.com/spf13/cobra"
)

type options struct {
	storage graph.Storage
}

func (o *options) AddFlags(_ *cobra.Command) {}

func (o *options) Run(_ *cobra.Command, args []string) error {
	sbomPath := args[0]
	// Ingest SBOM
	result, err := ingest.LoadDataFromPath(o.storage, sbomPath)
	if err != nil {
		return fmt.Errorf("failed to ingest SBOM: %w", err)
	}

	for index, data := range result {
		if err := ingest.SBOM(o.storage, data.Data); err != nil {
			return fmt.Errorf("failed to ingest SBOM: %w", err)
		}
		fmt.Printf("\r\033[1;36mIngested %d SBOMs\033[0m | \033[1;34m%s\033[0m", index+1, tools.TruncateString(data.Path, 50))
	}

	fmt.Println("\nSBOM ingested successfully")
	return nil
}

func New(storage graph.Storage) *cobra.Command {
	o := &options{
		storage: storage,
	}
	cmd := &cobra.Command{
		Use:               "sbom [sbomPath]",
		Short:             "Ingest an SBOM into storage",
		Args:              cobra.ExactArgs(1),
		RunE:              o.Run,
		DisableAutoGenTag: true,
	}
	o.AddFlags(cmd)

	return cmd
}
