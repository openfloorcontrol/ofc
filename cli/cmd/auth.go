package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
every ofc run that uses the furniture, including ones already running.

After consent the browser is sent to a callback on a free loopback port of
this machine. If the browser runs elsewhere, that page won't load: paste
its address into ofc auth, which delivers it. Alternatively set
$OFC_OAUTH_CALLBACK to a URL that reaches this machine and that the
authorization server accepts; ofc listens on its port while it waits.`,
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
	f.OAuthCallbackURL = os.Getenv("OFC_OAUTH_CALLBACK")
	pasting := false
	f.OAuthConsent = func(_, authURL string) {
		fmt.Printf("Open this URL to authorize %s:\n  %s\n\n", name, authURL)
		fmt.Printf("If the browser can't reach this machine afterwards, paste the address it\n" +
			"was sent to (from its address bar) here and press Enter:\n")
		if !pasting {
			pasting = true
			go forwardPastedCallbacks(os.Stdin, os.Stdout)
		}
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

// forwardPastedCallbacks reads callback URLs pasted into in and sends
// each to the callback listener of this ofc, as the browser would have —
// for a browser on another machine that can't reach the loopback
// callback. The listener checks the state and answers; its answer goes
// to out.
func forwardPastedCallbacks(in io.Reader, out io.Writer) {
	lines := bufio.NewScanner(in)
	for lines.Scan() {
		pasted := strings.TrimSpace(lines.Text())
		if pasted == "" {
			continue
		}
		u, err := url.Parse(pasted)
		if err != nil || u.Scheme != "http" || u.Query().Get("code") == "" {
			fmt.Fprintln(out, "That is not a callback address (http://…/callback?code=…&state=…). Try again:")
			continue
		}
		resp, err := http.Get(pasted)
		if err != nil {
			fmt.Fprintf(out, "Could not deliver it: %v. Try again:\n", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Fprintln(out, strings.TrimSpace(string(body)))
	}
}
