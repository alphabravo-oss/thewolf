package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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
	in := bufio.NewScanner(cmd.InOrStdin())
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	protocolVersion := ""
	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
				URI  string `json:"uri"`
			} `json:"params"`
		}
		if err := json.Unmarshal(line, &message); err != nil {
			return fmt.Errorf("invalid MCP message: %w", err)
		}
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, c.requestURL("/mcp"), bytes.NewReader(line))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if protocolVersion != "" {
			req.Header.Set("Mcp-Protocol-Version", protocolVersion)
		}
		req.Header.Set("Mcp-Method", message.Method)
		if message.Params.Name != "" {
			req.Header.Set("Mcp-Name", message.Params.Name)
		}
		if message.Method == "resources/read" {
			req.Header.Set("Mcp-Name", message.Params.URI)
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read MCP response: %w", readErr)
		}
		if len(body) > 16<<20 {
			return fmt.Errorf("MCP response exceeds 16 MiB")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &APIError{StatusCode: resp.StatusCode, Message: "MCP request rejected"}
		}
		if len(message.ID) == 0 && len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return fmt.Errorf("empty MCP response")
		}
		var reply struct {
			JSONRPC string `json:"jsonrpc"`
			Result  struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &reply); err != nil || reply.JSONRPC != "2.0" {
			return fmt.Errorf("invalid MCP response")
		}
		if message.Method == "initialize" && reply.Result.ProtocolVersion != "" {
			protocolVersion = reply.Result.ProtocolVersion
		}
		if _, err := cmd.OutOrStdout().Write(append(bytes.TrimSpace(body), '\n')); err != nil {
			return err
		}
	}
	return in.Err()
}
