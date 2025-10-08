package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

func checkpointDockerContainer(containerID, checkpointDir string) error {
	ctx := context.Background()

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	if !containerInfo.State.Running {
		return fmt.Errorf("container %s is not running", containerID)
	}

	pid := containerInfo.State.Pid
	if pid == 0 {
		return fmt.Errorf("could not get PID for container %s", containerID)
	}

	fmt.Printf("Container PID: %d\n", pid)

	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Save container metadata
	metadataFile := filepath.Join(checkpointDir, "container.info")
	metadata := fmt.Sprintf("CONTAINER_ID=%s\nCONTAINER_NAME=%s\nIMAGE=%s\nPID=%d\n",
		containerID,
		containerInfo.Name,
		containerInfo.Config.Image,
		pid)

	if err := os.WriteFile(metadataFile, []byte(metadata), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// For Docker containers, we need a more specialized approach
	return checkpointDockerProcess(pid, checkpointDir, containerInfo.GraphDriver.Name)
}

func checkpointDockerProcess(pid int, checkpointDir string, graphDriver string) error {
	criuClient := criu.MakeCriu()

	_, err := criuClient.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to get CRIU version (is CRIU installed?): %w", err)
	}
	fmt.Printf("CRIU version check passed\n")

	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	// Docker-specific CRIU options
	opts := &rpc.CriuOpts{
		Pid:          proto.Int32(int32(pid)),
		ImagesDirFd:  proto.Int32(int32(imageDir.Fd())),
		LogLevel:     proto.Int32(4),
		LogFile:      proto.String("dump.log"),
		LeaveRunning: proto.Bool(true),
		GhostLimit:   proto.Uint32(10000000),
		// Docker-specific options
		External: []string{
			"mnt[]:m",  // Allow missing mounts
			"mnt[/proc/sys]:m",
			"mnt[/proc/sysrq-trigger]:m",
			"mnt[/proc/irq]:m",
			"mnt[/proc/bus]:m",
			"mnt[/sys/firmware]:m",
			"dev[]",    // External devices
		},
		// Handle Docker's complex mount structure
		AutoExtMnt:     proto.Bool(true),
		ExtMountMap:    proto.String("/proc/mounts"),
		ForceIrmap:     proto.Bool(true),
	}

	// Add process-specific options
	if err := prepareProcessForDump(pid, opts); err != nil {
		return fmt.Errorf("failed to prepare process for dump: %w", err)
	}

	notify := NewNotifyHandler(true)

	fmt.Println("Creating Docker checkpoint...")
	err = criuClient.Dump(opts, notify)
	if err != nil {
		logPath := filepath.Join(checkpointDir, "dump.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log output:\n%s\n", string(logData))
		}

		// Try alternative approach if first attempt fails
		fmt.Println("First attempt failed, trying with minimal options...")
		return checkpointWithMinimalOptions(pid, checkpointDir)
	}

	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	fmt.Printf("Checkpoint created with %d files\n", len(entries))
	fmt.Println("Checkpoint files:")
	for _, entry := range entries {
		info, _ := entry.Info()
		fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
	}

	return nil
}

func checkpointWithMinimalOptions(pid int, checkpointDir string) error {
	// Clean up previous attempt
	os.Remove(filepath.Join(checkpointDir, "dump.log"))

	criuClient := criu.MakeCriu()

	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	// Minimal options with external mount handling
	opts := &rpc.CriuOpts{
		Pid:          proto.Int32(int32(pid)),
		ImagesDirFd:  proto.Int32(int32(imageDir.Fd())),
		LogLevel:     proto.Int32(4),
		LogFile:      proto.String("dump-minimal.log"),
		LeaveRunning: proto.Bool(true),
		// Tell CRIU to ignore problematic mounts
		External: []string{
			"mnt[]",     // Treat all mounts as external
		},
	}

	notify := NewNotifyHandler(false) // Less verbose

	fmt.Println("Attempting checkpoint with minimal options...")
	err = criuClient.Dump(opts, notify)
	if err != nil {
		logPath := filepath.Join(checkpointDir, "dump-minimal.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU minimal log output:\n%s\n", string(logData))
		}
		return fmt.Errorf("checkpoint failed even with minimal options: %w", err)
	}

	return nil
}