# nzbgrab

A fast, parallel NZB downloader for Usenet with automatic PAR2 verification, archive extraction, and file deobfuscation.

## Features

- **Parallel downloads** - Downloads multiple segments and NZBs concurrently
- **PAR2 verification & repair** - Automatically verifies and repairs files using PAR2
- **Archive extraction** - Extracts RAR, ZIP, and 7z archives (including nested archives)
- **File deobfuscation** - Renames obfuscated filenames using the NZB name
- **Automatic cleanup** - Removes PAR2 files, archives, and source NZB after successful processing
- **Bandwidth limiting** - Optional speed limit with K/M/G suffixes
- **SSL support** - Automatically uses SSL for port 563

## Installation

### Homebrew (macOS/Linux)

```bash
brew install andyjeffries/tap/nzbgrab
```

### Arch Linux (AUR)

```bash
yay -S nzbgrab
```

### Ubuntu/Debian

Download the `.deb` from [GitHub Releases](https://github.com/andyjeffries/nzbgrab/releases):

```bash
curl -LO https://github.com/andyjeffries/nzbgrab/releases/latest/download/nzbgrab_0.1.1_amd64.deb
sudo dpkg -i nzbgrab_0.1.1_amd64.deb
```

### Build from source

Requires Go 1.21 or later.

```bash
git clone https://github.com/andyjeffries/nzbgrab
cd nzbgrab
make build
sudo make install
```

### External dependencies

For full functionality, install these tools:

- `par2` - PAR2 verification and repair
- `unrar` - RAR extraction
- `7z` - 7-Zip extraction
- `unzip` - ZIP extraction

On Arch Linux:
```bash
pacman -S par2cmdline unrar p7zip unzip
```

On Ubuntu/Debian:
```bash
apt install par2 unrar p7zip-full unzip
```

On macOS:
```bash
brew install par2 unrar p7zip
```

## Configuration

Create a config file at `~/.config/nzbgrab/config.toml`:

```toml
[server]
host = "news.example.com"
port = 563
username = "your_username"
password = "your_password"
connections = 10

[download]
dir = "~/Downloads"
parallel = 2
```

### Config options

| Option | Description | Default |
|--------|-------------|---------|
| `server.host` | NNTP server hostname | (required) |
| `server.port` | NNTP server port (563 = SSL, 119 = plain) | 563 |
| `server.username` | NNTP username | (required) |
| `server.password` | NNTP password | (required) |
| `server.connections` | Concurrent connections per NZB | 10 |
| `download.dir` | Default download directory | ~/Downloads |
| `download.parallel` | Number of NZBs to download in parallel | 2 |

## Usage

```bash
# Download a single NZB
nzbgrab movie.nzb

# Download multiple NZBs
nzbgrab *.nzb

# Specify output directory
nzbgrab -o /media/movies movie.nzb

# Limit bandwidth to 50 MB/s
nzbgrab -l 50M movie.nzb

# Skip extraction (keep archives)
nzbgrab -n movie.nzb

# Use a different config file
nzbgrab -c /path/to/config.toml movie.nzb

# Quiet mode (less output)
nzbgrab -q movie.nzb
```

### Command-line options

| Option | Description |
|--------|-------------|
| `-o, --output` | Output directory (overrides config) |
| `-l, --limit` | Bandwidth limit (e.g., 10M, 500K, 1G) |
| `-p, --parallel` | Number of parallel NZB downloads |
| `-n, --no-extract` | Skip archive extraction |
| `-c, --config` | Path to config file |
| `-q, --quiet` | Suppress progress output |

## How it works

1. **Download** - Segments are downloaded in parallel using multiple NNTP connections
2. **Decode** - yEnc-encoded segments are decoded and assembled into files
3. **Verify** - PAR2 files are used to verify integrity and repair if needed
4. **Extract** - Archives are extracted recursively (handles nested archives)
5. **Cleanup** - PAR2 files, archives, and the source NZB are removed
6. **Deobfuscate** - Files with random alphanumeric names are renamed using the NZB name

## Performance

Typical speeds with 10 connections on a good Usenet provider:

- ~80-120 MB/s on fast connections
- Parallel NZB downloads for efficient queue processing

## Development

### Building

```bash
make build      # Build for current platform
make test       # Run tests
make clean      # Remove build artifacts
```

### Releasing

```bash
# Bump version (choose one)
make bump-patch   # 0.1.0 -> 0.1.1
make bump-minor   # 0.1.0 -> 0.2.0
make bump-major   # 0.1.0 -> 1.0.0

# Create release (builds, tags, pushes, creates GitHub release)
make release

# Update package repositories
make homebrew-bump   # Update Homebrew formula
make aur-publish     # Update AUR package
```

### Package repository setup

**Homebrew tap** (one-time):
```bash
# Clone your tap repo
git clone git@github.com:andyjeffries/homebrew-tap.git ../homebrew-tap
```

**AUR** (one-time):
```bash
# Clone AUR repo (requires AUR account with SSH key)
git clone ssh://aur@aur.archlinux.org/nzbgrab.git ../nzbgrab-aur
```

## License

MIT
