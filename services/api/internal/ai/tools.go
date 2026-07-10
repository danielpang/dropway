// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/sandbox"
)

// The model's entire tool surface is the sandbox: run commands and read/write/
// list files in the working tree. It has NO tool that reaches the Dropway API
// (no create-site, publish, deploy) — deploy-as-draft and publish happen
// API-side, so a prompt-injected model can at worst produce a bad draft, which
// the human preview step catches.
const (
	toolRunCommand = "run_command"
	toolWriteFile  = "write_file"
	toolReadFile   = "read_file"
	toolListFiles  = "list_files"
)

// builderTools is the JSON-schema tool set advertised to the model each turn.
func builderTools() []openrouter.Tool {
	return []openrouter.Tool{
		{Type: "function", Function: openrouter.ToolSpec{
			Name:        toolRunCommand,
			Description: "Run a shell command in the site's working directory (e.g. npm install, a build). Returns stdout, stderr, and the exit code.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"command":{"type":"string","description":"The shell command to run."}
				},
				"required":["command"]
			}`),
		}},
		{Type: "function", Function: openrouter.ToolSpec{
			Name:        toolWriteFile,
			Description: "Create or overwrite a file in the site, relative to the site root (e.g. index.html, assets/app.js).",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"path":{"type":"string"},
					"content":{"type":"string"}
				},
				"required":["path","content"]
			}`),
		}},
		{Type: "function", Function: openrouter.ToolSpec{
			Name:        toolReadFile,
			Description: "Read a file from the site, relative to the site root.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{"path":{"type":"string"}},
				"required":["path"]
			}`),
		}},
		{Type: "function", Function: openrouter.ToolSpec{
			Name:        toolListFiles,
			Description: "List the files currently in the site (optionally under a subdirectory).",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{"dir":{"type":"string","description":"Subdirectory, or empty for the site root."}}
			}`),
		}},
	}
}

func mustSchema(s string) json.RawMessage { return json.RawMessage(s) }

// dispatchTool executes one assistant tool call against the sandbox and returns
// the tool result content (a compact human/JSON string the model reads next
// turn). Errors are returned as content, not Go errors: a failed command is
// information the model should see and react to, not a turn-ending failure.
func dispatchTool(ctx context.Context, sb sandbox.Sandbox, call openrouter.ToolCall) string {
	switch call.Function.Name {
	case toolRunCommand:
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || args.Command == "" {
			return "error: invalid arguments for run_command"
		}
		res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", args.Command}, Cwd: ""})
		if err != nil {
			return "error running command: " + err.Error()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "exit_code: %d\n", res.ExitCode)
		if len(res.Stdout) > 0 {
			fmt.Fprintf(&b, "stdout:\n%s\n", res.Stdout)
		}
		if len(res.Stderr) > 0 {
			fmt.Fprintf(&b, "stderr:\n%s\n", res.Stderr)
		}
		if res.Truncated {
			b.WriteString("(output truncated)\n")
		}
		return b.String()

	case toolWriteFile:
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || args.Path == "" {
			return "error: invalid arguments for write_file"
		}
		if err := sb.WriteFile(ctx, args.Path, []byte(args.Content)); err != nil {
			return "error writing file: " + err.Error()
		}
		return fmt.Sprintf("wrote %s (%d bytes)", args.Path, len(args.Content))

	case toolReadFile:
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || args.Path == "" {
			return "error: invalid arguments for read_file"
		}
		data, err := sb.ReadFile(ctx, args.Path)
		if err != nil {
			return "error reading file: " + err.Error()
		}
		return string(data)

	case toolListFiles:
		var args struct {
			Dir string `json:"dir"`
		}
		_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		infos, err := sb.ListFiles(ctx, args.Dir)
		if err != nil {
			return "error listing files: " + err.Error()
		}
		var b strings.Builder
		for _, f := range infos {
			kind := "file"
			if f.IsDir {
				kind = "dir"
			}
			fmt.Fprintf(&b, "%s\t%s\t%d\n", kind, f.Path, f.Size)
		}
		if b.Len() == 0 {
			return "(empty)"
		}
		return b.String()

	default:
		return "error: unknown tool " + call.Function.Name
	}
}
