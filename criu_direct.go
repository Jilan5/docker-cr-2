package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

// checkpointContainerDirect bypasses Docker and uses CRIU directly
func checkpointContainerDirect(containerID, checkpointDir string) error {
	ctx := context.Background()

	// Get container info from Docker
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	if !containerInfo.State.Running {
		return fmt.Errorf("container %s is not running", containerID)
	}

	pid := containerInfo.State.Pid
	fmt.Printf("Container PID: %d\n", pid)

	// Create checkpoint directory
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Save container metadata for restore
	metadataFile := filepath.Join(checkpointDir, "container.meta")
	metadata := fmt.Sprintf("CONTAINER_ID=%s\nCONTAINER_NAME=%s\nIMAGE=%s\nPID=%d\n",
		containerInfo.ID,
		containerInfo.Name,
		containerInfo.Config.Image,
		pid)

	if err := os.WriteFile(metadataFile, []byte(metadata), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// Use CRIU directly on the container process
	return checkpointProcessDirect(pid, checkpointDir)
}

func checkpointProcessDirect(pid int, checkpointDir string) error {
	criuClient := criu.MakeCriu()

	// Check CRIU version
	if _, err := criuClient.GetCriuVersion(); err != nil {
		return fmt.Errorf("CRIU check failed: %w", err)
	}

	// Prepare CRIU
	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	// Open checkpoint directory
	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	// CRIU options for container checkpoint
	opts := &rpc.CriuOpts{
		Pid:          proto.Int32(int32(pid)),
		ImagesDirFd:  proto.Int32(int32(imageDir.Fd())),
		LogLevel:     proto.Int32(4),
		LogFile:      proto.String("dump.log"),
		LeaveRunning: proto.Bool(true),
		TcpEstablished: proto.Bool(true),
		ExtUnixSk:     proto.Bool(true),
		ShellJob:      proto.Bool(false),
		// Container-specific options
		External: []string{
			"mnt[]",     // Handle all mounts as external
		},
	}

	// Create notification handler
	notify := &SimpleNotify{}

	fmt.Println("Creating checkpoint with CRIU...")
	startTime := time.Now()

	err = criuClient.Dump(opts, notify)
	if err != nil {
		// Read and display log
		logPath := filepath.Join(checkpointDir, "dump.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log:\n%s\n", string(logData))
		}
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	duration := time.Since(startTime)
	fmt.Printf("Checkpoint completed in %.3f seconds\n", duration.Seconds())

	// List created files
	entries, _ := os.ReadDir(checkpointDir)
	fmt.Printf("Created %d checkpoint files\n", len(entries))

	return nil
}

// restoreContainerDirect restores using CRIU directly
func restoreContainerDirect(containerID, checkpointDir string) error {
	ctx := context.Background()

	// Get Docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Stop container if running
	if info, err := dockerClient.ContainerInspect(ctx, containerID); err == nil {
		if info.State.Running {
			fmt.Println("Stopping container...")
			timeout := 10
			stopOpts := container.StopOptions{
				Timeout: &timeout,
			}
			dockerClient.ContainerStop(ctx, containerID, stopOpts)
			time.Sleep(2 * time.Second)
		}
	}

	// Use CRIU to restore
	return restoreProcessDirect(checkpointDir)
}

func restoreProcessDirect(checkpointDir string) error {
	criuClient := criu.MakeCriu()

	// Check CRIU version
	if _, err := criuClient.GetCriuVersion(); err != nil {
		return fmt.Errorf("CRIU check failed: %w", err)
	}

	// Prepare CRIU
	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	// Open checkpoint directory
	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	// CRIU restore options
	opts := &rpc.CriuOpts{
		ImagesDirFd:    proto.Int32(int32(imageDir.Fd())),
		LogLevel:       proto.Int32(4),
		LogFile:        proto.String("restore.log"),
		TcpEstablished: proto.Bool(true),
		ExtUnixSk:      proto.Bool(true),
		ShellJob:       proto.Bool(false),
		// Container-specific options
		External: []string{
			"mnt[]",     // Handle all mounts as external
		},
	}

	// Create notification handler
	notify := &SimpleNotify{}

	fmt.Println("Restoring with CRIU...")
	startTime := time.Now()

	err = criuClient.Restore(opts, notify)
	if err != nil {
		// Read and display log
		logPath := filepath.Join(checkpointDir, "restore.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log:\n%s\n", string(logData))
		}
		return fmt.Errorf("restore failed: %w", err)
	}

	duration := time.Since(startTime)
	fmt.Printf("Restore completed in %.3f seconds\n", duration.Seconds())

	return nil
}

// SimpleNotify implements the Notify interface
type SimpleNotify struct{}

func (n *SimpleNotify) PreDump() error { return nil }
func (n *SimpleNotify) PostDump() error { return nil }
func (n *SimpleNotify) PreRestore() error { return nil }
func (n *SimpleNotify) PostRestore(pid int32) error {
	fmt.Printf("Process restored with PID: %d\n", pid)
	return nil
}
func (n *SimpleNotify) NetworkLock() error { return nil }
func (n *SimpleNotify) NetworkUnlock() error { return nil }
func (n *SimpleNotify) SetupNamespaces(pid int32) error { return nil }
func (n *SimpleNotify) PostSetupNamespaces() error { return nil }
func (n *SimpleNotify) PostResume() error { return nil }