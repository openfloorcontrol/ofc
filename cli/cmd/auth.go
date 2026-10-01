package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/furniture"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth <furniture>",
	Short: "Authorize an OAuth MCP furniture",
	Long: `Connect to an MCP furniture with an oauth: block and, if it needs it, run
the OAuth consent: open the printed URL and approve. The token is stored in
~/.ofc/oauth/<floor>/<furniture>.json (or $OFC_OAUTH_DIR) and refreshed by
every ofc run that uses the furniture, including ones already running.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := authorize(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	authCmd.Flags().StringVarP(&blueprintFile, "file", "f", "blueprint.yaml", "Blueprint file")
	rootCmd.AddCommand(authCmd)
}

func authorize(name string) error {
	bp, err := blueprint.Load(blueprintFile)
	if err != nil {
		return fmt.Errorf("load blueprint: %w", err)
	}
	var fd *blueprint.FurnitureDef
	for i := range bp.Furniture {
		if bp.Furniture[i].Name == name {
			fd = &bp.Furniture[i]
		}
	}
	if fd == nil {
		return fmt.Errorf("blueprint %q has no furniture %q", bp.Name, name)
	}
	if fd.OAuth == nil {
		return fmt.Errorf("furniture %q has no oauth: block", name)
	}

	dir, err := defaultOAuthDir()
	if err != nil {
		return err
	}
	f := floor.NewFloor(bp)
	f.OAuthDir = dir
	f.OAuthConsent = func(_, authURL string) {
		fmt.Printf("Open this URL to authorize %s:\n  %s\n", name, authURL)
	}
	handler, err := f.OAuthHandler(*fd)
	if err != nil {
		return err
	}

	m, err := furniture.NewExternalMCPFromURL(context.Background(), name, fd.URL, fd.Headers, handler)
	if err != nil {
		return err
	}
	defer m.Close()
	fmt.Printf("%s is authorized (%d tools)", name, len(m.Tools()))
	if fd.OAuth.Grant != "client_credentials" {
		fmt.Printf("; token in %s", filepath.Join(dir, bp.Name, name+".json"))
	}
	fmt.Println()
	return nil
}
