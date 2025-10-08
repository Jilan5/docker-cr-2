# Cedana Process Analysis

## Cedana's Actual Implementation

After reviewing the Cedana codebase, here's what they actually implement:

### For Docker Containers (`docker-dump.go`)
- **Uses Docker's native checkpoint API**: `dc.Docker.CheckpointCreate()`
- Line 54: `err = dc.Docker.CheckpointCreate(cmd.Context(), container, opts)`
- This is Docker's experimental checkpoint feature that uses CRIU internally
- **Key insight**: They don't bypass Docker, they use Docker's built-in checkpoint capability

### For Regular Processes (`client-dump.go`)
- **Uses direct CRIU**: `c.CRIU.Dump(opts, nfy)`
- Line 131: `err = c.CRIU.Dump(opts, nfy)`
- This bypasses Docker entirely and uses go-criu library directly
- **Key insight**: Direct CRIU works well for simple processes, not containers

## My Previous Confusion

I incorrectly conflated these two approaches and made contradictory statements:

1. **First I said**: Cedana uses direct CRIU
2. **Later I said**: They use Docker's native API
3. **Truth**: Both were partially correct but I failed to distinguish between their **docker** vs **process** checkpoint implementations

## The Hybrid Truth

Cedana is **hybrid**:

- **Docker containers**: Uses Docker's native checkpoint API (which internally uses CRIU)
- **Regular processes**: Uses direct CRIU via go-criu library

## Why This Approach Works

1. **For containers**: Docker's API handles all the namespace complexity internally
2. **For processes**: Direct CRIU works because there's no container namespace complexity
3. **Smart separation**: They don't try to use direct CRIU on containers (which we proved is extremely difficult)

## Key Cedana Process Features

### Process Analysis (`prepare_dump` function)
```go
// Check file descriptors
open_files, err := p.OpenFiles()

// Check network connections
conns, err := p.Connections()
for _, conn := range conns {
    if conn.Type == syscall.SOCK_STREAM { // TCP
        hasTCP = true
    }
    if conn.Type == syscall.AF_UNIX { // interprocess
        hasExtUnixSocket = true
    }
}

// Set CRIU options based on analysis
opts.TcpEstablished = proto.Bool(hasTCP)
opts.ExtUnixSk = proto.Bool(hasExtUnixSocket)
```

### CRIU Options (`prepare_opts` function)
```go
opts := rpc.CriuOpts{
    LogLevel:     proto.Int32(4),
    LogFile:      proto.String("dump.log"),
    ShellJob:     proto.Bool(false),
    LeaveRunning: proto.Bool(true),  // Keep process running
    GhostLimit:   proto.Uint32(uint32(10000000)),
    ExtMasters:   proto.Bool(true),
}
```

## Lesson Learned

**Cedana succeeds because they use the right tool for the right job:**
- Docker API for Docker containers (leverages Docker's internal CRIU handling)
- Direct CRIU for simple processes (avoids container complexity)

**Our failure was trying to use direct CRIU on containers**, which is fundamentally more complex than Cedana's approach of using Docker's native checkpoint API for containers.