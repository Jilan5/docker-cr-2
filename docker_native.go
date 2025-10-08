package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// checkpointDockerNative uses Docker's native checkpoint feature (like Cedana does)
func checkpointDockerNative(containerID, checkpointDir string) error {
	ctx := context.Background()

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Verify container exists and is running
	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	if !containerInfo.State.Running {
		return fmt.Errorf("container %s is not running", containerID)
	}

	fmt.Printf("Container %s is running with PID %d\n", containerID, containerInfo.State.Pid)

	// Create checkpoint directory if needed
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Use Docker's checkpoint API (this is what Cedana does)
	// Handle container IDs of different lengths safely and make unique
	shortID := containerID
	if len(containerID) > 12 {
		shortID = containerID[:12]
	}

	// Create unique checkpoint ID with timestamp
	timestamp := time.Now().Unix()
	checkpointID := fmt.Sprintf("checkpoint-%s-%d", shortID, timestamp)

	// Cleanup any existing checkpoints for this container first
	cleanupExistingCheckpoints(dockerClient, ctx, containerID)

	opts := types.CheckpointCreateOptions{
		CheckpointID:  checkpointID,
		// Don't specify CheckpointDir - let Docker use its default location
		Exit:          false, // Keep container running (like LeaveRunning in CRIU)
	}

	fmt.Printf("Creating Docker checkpoint '%s' in %s...\n", checkpointID, checkpointDir)

	err = dockerClient.CheckpointCreate(ctx, containerID, opts)
	if err != nil {
		// Extract dump log path from error if available (Cedana's approach)
		re := regexp.MustCompile("path= (.*): ")
		matches := re.FindStringSubmatch(fmt.Sprintf("%s", err))
		if len(matches) >= 2 {
			dumpLog := matches[1]
			fmt.Printf("Dump log path: %s\n", dumpLog)

			// Try to read and display the dump log
			cmd := exec.Command("cat", dumpLog)
			output, _ := cmd.CombinedOutput()
			if len(output) > 0 {
				fmt.Printf("CRIU dump log:\n%s\n", string(output))
			}
		}

		return fmt.Errorf("Docker checkpoint failed: %w", err)
	}

	fmt.Println("Docker checkpoint created successfully!")

	// Copy checkpoint files from Docker's default location to our custom directory
	dockerCheckpointDir := fmt.Sprintf("/var/lib/docker/containers/%s/checkpoints/%s", containerInfo.ID, checkpointID)
	userCheckpointPath := filepath.Join(checkpointDir, checkpointID)

	fmt.Printf("Copying checkpoint files from Docker storage to %s...\n", userCheckpointPath)
	if err := copyCheckpointFiles(dockerCheckpointDir, userCheckpointPath); err != nil {
		fmt.Printf("Warning: Could not copy checkpoint files: %v\n", err)
		fmt.Printf("Checkpoint created but files remain in Docker's internal storage\n")
	} else {
		// List checkpoint files
		if entries, err := os.ReadDir(userCheckpointPath); err == nil {
			fmt.Printf("Checkpoint files in %s:\n", userCheckpointPath)
			for _, entry := range entries {
				info, _ := entry.Info()
				fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
			}
		}
	}

	// Save metadata
	metadataFile := filepath.Join(checkpointDir, "docker-checkpoint.info")
	metadata := fmt.Sprintf("CONTAINER_ID=%s\nCHECKPOINT_ID=%s\nIMAGE=%s\n",
		containerID,
		checkpointID,
		containerInfo.Config.Image)

	if err := os.WriteFile(metadataFile, []byte(metadata), 0644); err != nil {
		fmt.Printf("Warning: failed to write metadata: %v\n", err)
	}

	return nil
}

// restoreDockerNative uses Docker's native restore feature
func restoreDockerNative(containerID, checkpointDir string) error {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Read metadata to get checkpoint ID
	metadataFile := filepath.Join(checkpointDir, "docker-checkpoint.info")
	metadataBytes, err := os.ReadFile(metadataFile)
	if err != nil {
		// Try to guess checkpoint ID
		entries, _ := os.ReadDir(checkpointDir)
		for _, entry := range entries {
			if entry.IsDir() && len(entry.Name()) > 10 {
				checkpointID := entry.Name()
				fmt.Printf("Found checkpoint directory: %s\n", checkpointID)

				// Try to restore with this checkpoint
				return restoreWithCheckpoint(dockerClient, containerID, checkpointID, checkpointDir)
			}
		}
		return fmt.Errorf("no checkpoint found in %s", checkpointDir)
	}

	// Parse checkpoint ID from metadata
	var checkpointID string
	metadata := string(metadataBytes)
	re := regexp.MustCompile(`CHECKPOINT_ID=(.+)`)
	if matches := re.FindStringSubmatch(metadata); len(matches) >= 2 {
		checkpointID = matches[1]
	}

	if checkpointID == "" {
		return fmt.Errorf("could not determine checkpoint ID")
	}

	return restoreWithCheckpoint(dockerClient, containerID, checkpointID, checkpointDir)
}

func restoreWithCheckpoint(dockerClient *client.Client, containerID, checkpointID, checkpointDir string) error {
	ctx := context.Background()

	fmt.Printf("Restoring container %s from checkpoint %s...\n", containerID, checkpointID)

	// Stop and remove container if it exists
	if info, err := dockerClient.ContainerInspect(ctx, containerID); err == nil {
		if info.State.Running {
			fmt.Println("Stopping running container...")
			timeout := 10
			stopOpts := container.StopOptions{
				Timeout: &timeout,
			}
			dockerClient.ContainerStop(ctx, containerID, stopOpts)
		}

		fmt.Println("Removing existing container...")
		removeOpts := container.RemoveOptions{
			Force: true,
		}
		dockerClient.ContainerRemove(ctx, containerID, removeOpts)
	}

	// Start container with checkpoint
	startOpts := types.ContainerStartOptions{
		CheckpointID:  checkpointID,
		// Don't specify CheckpointDir - let Docker use its default location
	}

	err := dockerClient.ContainerStart(ctx, containerID, startOpts)
	if err != nil {
		return fmt.Errorf("failed to restore container from checkpoint: %w", err)
	}

	// Verify container is running
	info, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container: %w", err)
	}

	if info.State.Running {
		fmt.Printf("Container restored successfully! PID: %d\n", info.State.Pid)
	} else {
		return fmt.Errorf("container restored but not running, state: %s", info.State.Status)
	}

	return nil
}

// listDockerCheckpoints lists all checkpoints for a container
func listDockerCheckpoints(containerID string) error {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	ctx := context.Background()
	checkpoints, err := dockerClient.CheckpointList(ctx, containerID, types.CheckpointListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		fmt.Printf("No checkpoints found for container %s\n", containerID)
		return nil
	}

	fmt.Printf("Checkpoints for container %s:\n", containerID)
	for _, cp := range checkpoints {
		fmt.Printf("  - %s\n", cp.Name)
	}

	return nil
}

// cleanupExistingCheckpoints removes existing checkpoints for a container
func cleanupExistingCheckpoints(dockerClient *client.Client, ctx context.Context, containerID string) {
	checkpoints, err := dockerClient.CheckpointList(ctx, containerID, types.CheckpointListOptions{})
	if err != nil {
		// If we can't list checkpoints, just continue
		return
	}

	for _, checkpoint := range checkpoints {
		fmt.Printf("Removing existing checkpoint: %s\n", checkpoint.Name)
		dockerClient.CheckpointDelete(ctx, containerID, types.CheckpointDeleteOptions{
			CheckpointID: checkpoint.Name,
		})
	}
}

// copyCheckpointFiles copies checkpoint files from Docker's internal storage to user directory
func copyCheckpointFiles(srcDir, dstDir string) error {
	// Create destination directory
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	// Use cp command to copy files (handles permissions properly)
	cmd := exec.Command("cp", "-r", srcDir+"/.", dstDir)
	return cmd.Run()
}