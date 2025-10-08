package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

func restoreContainer(containerID, checkpointDir string) error {
	metadataFile := filepath.Join(checkpointDir, "container.info")
	metadataBytes, err := os.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("failed to read metadata file: %w", err)
	}

	var originalImage string
	var originalPID int

	lines := strings.Split(string(metadataBytes), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "IMAGE=") {
			originalImage = strings.TrimPrefix(line, "IMAGE=")
		} else if strings.HasPrefix(line, "PID=") {
			pidStr := strings.TrimPrefix(line, "PID=")
			originalPID, _ = strconv.Atoi(pidStr)
		}
	}

	fmt.Printf("Original container image: %s\n", originalImage)
	fmt.Printf("Original PID: %d\n", originalPID)

	ctx := context.Background()
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err == nil {
		defer dockerClient.Close()
		if err := stopContainer(dockerClient, containerID); err != nil {
			fmt.Printf("Warning: failed to stop existing container: %v\n", err)
		}
	}

	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	fmt.Printf("Found %d checkpoint files\n", len(entries))
	hasCheckpoint := false
	for _, entry := range entries {
		if !entry.IsDir() {
			info, _ := entry.Info()
			fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
			if strings.HasSuffix(entry.Name(), ".img") {
				hasCheckpoint = true
			}
		}
	}

	if !hasCheckpoint {
		return fmt.Errorf("no checkpoint images found in %s", checkpointDir)
	}

	return restoreProcess(checkpointDir)
}

func restoreProcess(checkpointDir string) error {
	criuClient := criu.MakeCriu()

	version, err := criuClient.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to get CRIU version: %w", err)
	}
	fmt.Printf("CRIU version: %d.%d\n", version.Major, version.Minor)

	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	opts := &rpc.CriuOpts{
		ImagesDirFd: proto.Int32(int32(imageDir.Fd())),
		LogLevel:    proto.Int32(4),
		LogFile:     proto.String("restore.log"),
	}

	if err := prepareProcessForRestore(checkpointDir, opts); err != nil {
		return fmt.Errorf("failed to prepare for restore: %w", err)
	}

	notify := NewNotifyHandler(true)

	fmt.Println("Restoring process state with CRIU...")
	err = criuClient.Restore(opts, notify)
	if err != nil {
		logPath := filepath.Join(checkpointDir, "restore.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU restore log output:\n%s\n", string(logData))
		}
		return fmt.Errorf("CRIU restore failed: %w", err)
	}

	fmt.Println("CRIU restore completed successfully!")

	time.Sleep(2 * time.Second)

	return nil
}

func restoreSimpleProcess(checkpointDir string) error {
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	hasCheckpoint := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".img") {
			hasCheckpoint = true
			break
		}
	}

	if !hasCheckpoint {
		return fmt.Errorf("no checkpoint images found in %s", checkpointDir)
	}

	criuClient := criu.MakeCriu()

	version, err := criuClient.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to get CRIU version: %w", err)
	}
	fmt.Printf("CRIU version: %d.%d\n", version.Major, version.Minor)

	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	opts := &rpc.CriuOpts{
		ImagesDirFd:    proto.Int32(int32(imageDir.Fd())),
		LogLevel:       proto.Int32(4),
		LogFile:        proto.String("restore.log"),
		TcpEstablished: proto.Bool(true),
		ExtUnixSk:      proto.Bool(true),
		ShellJob:       proto.Bool(false),
	}

	notify := NewNotifyHandler(true)

	fmt.Println("Restoring process...")
	err = criuClient.Restore(opts, notify)
	if err != nil {
		logPath := filepath.Join(checkpointDir, "restore.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU restore log:\n%s\n", string(logData))
		}
		return fmt.Errorf("restore failed: %w", err)
	}

	fmt.Println("Process restored successfully!")
	return nil
}

func stopContainer(dockerClient *client.Client, containerID string) error {
	ctx := context.Background()

	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil
	}

	if containerInfo.State.Running {
		fmt.Printf("Stopping container %s...\n", containerID)
		timeout := 10
		stopOptions := container.StopOptions{
			Timeout: &timeout,
		}
		if err := dockerClient.ContainerStop(ctx, containerID, stopOptions); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	return nil
}