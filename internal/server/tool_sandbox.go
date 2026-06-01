package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type toolSandboxExecutor func(ctx context.Context, input toolSandboxInput) (toolSandboxResult, error)

type toolSandboxInput struct {
	UserID     string
	ToolID     string
	Tier       string
	Code       string
	ParamsJSON string
}

type toolSandboxResult struct {
	OK             bool   `json:"ok"`
	SandboxProfile string `json:"sandbox_profile"`
	APIMode        string `json:"api_mode"`
	Message        string `json:"message"`
	ExitCode       int    `json:"exit_code"`
	DurationMS     int64  `json:"duration_ms"`
	Stdout         string `json:"stdout,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	EchoParams     string `json:"echo_params,omitempty"`
}

type sandboxProfile struct {
	Name        string
	APIMode     string
	CPUs        string
	Memory      string
	PIDs        int
	Timeout     time.Duration
	NetworkNone bool
}

func toolSandboxProfileForTier(tier string) sandboxProfile {
	switch normalizeToolTier(tier) {
	case "T0":
		return sandboxProfile{
			Name:        "t0-light",
			APIMode:     "none",
			CPUs:        "0.25",
			Memory:      "128m",
			PIDs:        64,
			Timeout:     5 * time.Second,
			NetworkNone: true,
		}
	case "T1":
		return sandboxProfile{Name: "t1-light", APIMode: "colony-read", CPUs: "0.5", Memory: "256m", PIDs: 96, Timeout: 10 * time.Second}
	case "T2":
		return sandboxProfile{Name: "t2-strong", APIMode: "colony-readwrite", CPUs: "1", Memory: "512m", PIDs: 128, Timeout: 20 * time.Second}
	case "T3":
		return sandboxProfile{Name: "t3-strong", APIMode: "external-restricted", CPUs: "2", Memory: "1g", PIDs: 128, Timeout: 30 * time.Second}
	default:
		return sandboxProfile{Name: "t1-light", APIMode: "colony-read", CPUs: "0.5", Memory: "256m", PIDs: 96, Timeout: 10 * time.Second}
	}
}

func (s *Server) toolSandboxImage() string {
	image := strings.TrimSpace(s.cfg.ToolSandboxImage)
	if image == "" {
		return "alpine:3.21"
	}
	return image
}

func trimSandboxOutput(v string, limit int) string {
	if limit <= 0 {
		limit = 4096
	}
	v = strings.TrimSpace(v)
	if len(v) <= limit {
		return v
	}
	return v[:limit] + "\n...[truncated]"
}

func (s *Server) execToolInSandbox(parent context.Context, input toolSandboxInput) (toolSandboxResult, error) {
	profile := toolSandboxProfileForTier(input.Tier)
	result := toolSandboxResult{
		OK:             false,
		SandboxProfile: profile.Name,
		APIMode:        profile.APIMode,
		Message:        "sandbox execution failed",
		ExitCode:       -1,
	}
	code := strings.TrimSpace(input.Code)
	if code == "" {
		result.Message = "tool code is empty"
		return result, nil
	}
	timeout := profile.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	start := time.Now()
	args := []string{
		"run",
		"--rm",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(profile.PIDs),
		"--memory", profile.Memory,
		"--cpus", profile.CPUs,
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
		"-e", "TOOL_USER_ID=" + input.UserID,
		"-e", "TOOL_ID=" + input.ToolID,
		"-e", "TOOL_TIER=" + normalizeToolTier(input.Tier),
		"-e", "TOOL_API_MODE=" + profile.APIMode,
		"-e", "TOOL_PARAMS_JSON=" + input.ParamsJSON,
		"--entrypoint", "/bin/sh",
		s.toolSandboxImage(),
		"-lc", code,
	}
	if profile.NetworkNone {
		args = append(args[:1], append([]string{"--network", "none"}, args[1:]...)...)
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.DurationMS = time.Since(start).Milliseconds()
	result.Stdout = trimSandboxOutput(stdout.String(), 8000)
	result.Stderr = trimSandboxOutput(stderr.String(), 8000)
	if err == nil {
		result.OK = true
		result.ExitCode = 0
		result.Message = "sandbox executed"
		return result, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Message = "sandbox timeout"
		result.ExitCode = 124
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.Message = "sandbox exited with non-zero status"
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("tool sandbox invoke failed: %w", err)
}
