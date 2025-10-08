package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"google.golang.org/protobuf/proto"
)

type ProcessInfo struct {
	PID             int
	HasTCP          bool
	HasUnixSockets  bool
	HasPipes        bool
	HasEventfd      bool
	HasSignalfd     bool
	HasTimerfd      bool
	ProcessName     string
	State           string
}

func analyzeProcess(pid int) (*ProcessInfo, error) {
	info := &ProcessInfo{
		PID: pid,
	}

	if err := validateProcessExists(pid); err != nil {
		return nil, err
	}

	info.State = getProcessState(pid)
	info.ProcessName = getProcessName(pid)

	checkFileDescriptors(pid, info)

	checkNetworkConnections(pid, info)

	return info, nil
}

func validateProcessExists(pid int) error {
	statFile := fmt.Sprintf("/proc/%d/stat", pid)
	if _, err := os.Stat(statFile); os.IsNotExist(err) {
		return fmt.Errorf("process %d does not exist", pid)
	}
	return nil
}

func getProcessState(pid int) string {
	statFile := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statFile)
	if err != nil {
		return "unknown"
	}

	statStr := string(data)
	startParen := strings.Index(statStr, "(")
	endParen := strings.LastIndex(statStr, ")")

	if startParen != -1 && endParen != -1 && endParen > startParen {
		afterParen := statStr[endParen+2:]
		fields := strings.Fields(afterParen)
		if len(fields) > 0 {
			state := fields[0]
			switch state {
			case "R":
				return "running"
			case "S":
				return "sleeping"
			case "D":
				return "disk sleep"
			case "Z":
				return "zombie"
			case "T":
				return "stopped"
			case "t":
				return "tracing stop"
			case "X":
				return "dead"
			default:
				return state
			}
		}
	}

	return "unknown"
}

func getProcessName(pid int) string {
	cmdlineFile := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlineFile)
	if err != nil {
		return ""
	}

	cmdline := string(data)
	parts := strings.Split(cmdline, "\x00")
	if len(parts) > 0 {
		return parts[0]
	}

	return ""
}

func checkFileDescriptors(pid int, info *ProcessInfo) {
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		fdPath := fmt.Sprintf("%s/%s", fdDir, entry.Name())
		linkTarget, err := os.Readlink(fdPath)
		if err != nil {
			continue
		}

		if strings.HasPrefix(linkTarget, "pipe:") {
			info.HasPipes = true
		} else if strings.HasPrefix(linkTarget, "socket:") {
			info.HasUnixSockets = true
		} else if strings.HasPrefix(linkTarget, "anon_inode:[eventfd]") {
			info.HasEventfd = true
		} else if strings.HasPrefix(linkTarget, "anon_inode:[signalfd]") {
			info.HasSignalfd = true
		} else if strings.HasPrefix(linkTarget, "anon_inode:[timerfd]") {
			info.HasTimerfd = true
		}
	}
}

func checkNetworkConnections(pid int, info *ProcessInfo) {
	checkTCPConnections(fmt.Sprintf("/proc/%d/net/tcp", pid), info)
	checkTCPConnections(fmt.Sprintf("/proc/%d/net/tcp6", pid), info)

	checkUnixSockets(fmt.Sprintf("/proc/%d/net/unix", pid), info)
}

func checkTCPConnections(path string, info *ProcessInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		stateStr := fields[3]
		if state, err := strconv.ParseUint(stateStr, 16, 32); err == nil {
			if state == 0x01 {
				info.HasTCP = true
				return
			}
		}
	}
}

func checkUnixSockets(path string, info *ProcessInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 1 {
		info.HasUnixSockets = true
	}
}

func prepareProcessForDump(pid int, opts *rpc.CriuOpts) error {
	info, err := analyzeProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to analyze process: %w", err)
	}

	if info.State == "zombie" {
		return fmt.Errorf("cannot checkpoint zombie process")
	}

	fmt.Printf("Process analysis for PID %d:\n", pid)
	fmt.Printf("  Name: %s\n", info.ProcessName)
	fmt.Printf("  State: %s\n", info.State)
	fmt.Printf("  TCP connections: %v\n", info.HasTCP)
	fmt.Printf("  Unix sockets: %v\n", info.HasUnixSockets)
	fmt.Printf("  Pipes: %v\n", info.HasPipes)

	if info.HasTCP {
		opts.TcpEstablished = proto.Bool(true)
	}

	if info.HasUnixSockets {
		opts.ExtUnixSk = proto.Bool(true)
	}

	if opts.ShellJob == nil {
		if isShellJob(pid) {
			opts.ShellJob = proto.Bool(true)
		} else {
			opts.ShellJob = proto.Bool(false)
		}
	}

	return nil
}

func isShellJob(pid int) bool {
	pgid := syscall.Getpgid(pid)
	sid, _ := syscall.Getsid(pid)

	return pgid == sid
}

func prepareProcessForRestore(checkpointDir string, opts *rpc.CriuOpts) error {
	opts.TcpEstablished = proto.Bool(true)
	opts.ExtUnixSk = proto.Bool(true)
	opts.ShellJob = proto.Bool(false)

	return nil
}