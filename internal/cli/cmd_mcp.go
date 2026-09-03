package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "mcp",
		Short:       "MCP stdio JSON-RPC (server must set WOLF_MCP_ENABLED=1)",
		Annotations: apiAnno("POST", "/mcp"),
		Args:        cobra.NoArgs,
		RunE:        runMCPStdio,
	}
	return cmd
}

func runMCPStdio(cmd *cobra.Command, _ []string) error {
	c, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, c.requestURL("/mcp"), bytes.NewReader(line))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if len(bytes.TrimSpace(body)) == 0 {
			return fmt.Errorf("empty MCP response")
		}
		if _, err := os.Stdout.Write(append(bytes.TrimSpace(body), '\n')); err != nil {
			return err
		}
	}
	return in.Err()
}
