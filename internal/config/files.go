package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// maxTextBytesLimit keeps every editor text within one write request after JSON escaping
// (escaping grows text up to 6x; the small fields of the request get MaxSmallJSONBytes).
const maxTextBytesLimit = (filesapi.MaxJSONBytes - filesapi.MaxSmallJSONBytes) / 6

// FilesLimits are the limits of the files service (spec 3b §3.1, spec 4 §3.2).
type FilesLimits struct {
	MaxUploadBytes    int64 `toml:"max_upload_bytes"`
	MaxExtractBytes   int64 `toml:"max_extract_bytes"`
	MaxExtractEntries int64 `toml:"max_extract_entries"`
	ReserveBytes      int64 `toml:"reserve_bytes"`
	MaxTextBytes      int64 `toml:"max_text_bytes"`
	MaxListEntries    int   `toml:"max_list_entries"`
	MaxBackupBytes    int64 `toml:"max_backup_bytes"`   // content of one snapshot (tar)
	MaxBackupEntries  int64 `toml:"max_backup_entries"` // entries of one snapshot
	MaxBackupJobs     int   `toml:"max_backup_jobs"`    // concurrent restic jobs (backup, restore, forget)
}

// maxBackupJobsLimit bounds max_backup_jobs: every restic job holds its own memory and disk I/O.
const maxBackupJobsLimit = 8

// Files is the configuration of mcpanel-files (files.toml).
type Files struct {
	Dev          bool   `toml:"dev"`
	Socket       string `toml:"socket"`      // used only without systemd socket activation
	SocketMode   string `toml:"socket_mode"` // octal, default "0660"; used only without socket activation
	PanelUID     int    `toml:"panel_uid"`   // -1 = unset (only allowed with dev)
	InstancesDir string `toml:"instances_dir"`
	// ResticBinary is the restic executable (spec 4 §3.2): an absolute path; with dev also a
	// bare name that is looked up in PATH.
	ResticBinary string      `toml:"restic_binary"`
	Limits       FilesLimits `toml:"limits"`
}

func defaultFilesLimits() FilesLimits {
	return FilesLimits{
		MaxUploadBytes:    4 << 30,
		MaxExtractBytes:   20 << 30,
		MaxExtractEntries: 100000,
		ReserveBytes:      2 << 30,
		MaxTextBytes:      5 << 20,
		MaxListEntries:    10000,
		MaxBackupBytes:    64 << 30,
		MaxBackupEntries:  1_000_000,
		MaxBackupJobs:     1,
	}
}

func DefaultFiles() Files {
	return Files{
		Socket:       "/run/mc-panel-files/files.sock",
		SocketMode:   "0660",
		PanelUID:     -1,
		InstancesDir: DefaultInstancesDir,
		ResticBinary: "/usr/bin/restic",
		Limits:       defaultFilesLimits(),
	}
}

// DevFiles is the profile of `make dev`: same instance directories as the dev agent (so
// MCPANEL_DEV_DOCKER=1 shares them), the developer's own uid as the panel uid.
func DevFiles(root string) Files {
	f := DefaultFiles()
	f.Dev = true
	f.Socket = filepath.Join(root, ".dev", "files.sock")
	f.PanelUID = os.Getuid()
	f.InstancesDir = filepath.Join(root, ".dev", "mc")
	f.ResticBinary = "restic"
	return f
}

// SocketFileMode returns socket_mode as a file mode (0 if invalid; Validate rejects that).
func (f Files) SocketFileMode() os.FileMode {
	m, err := strconv.ParseUint(f.SocketMode, 8, 32)
	if err != nil || m > 0o777 {
		return 0
	}
	return os.FileMode(m)
}

// LoadFiles starts from DefaultFiles and overlays the TOML file at path; unknown keys are refused.
// Keys missing in the file (also single [limits] keys) keep their defaults: toml decodes into the
// pre-filled struct.
func LoadFiles(path string) (Files, error) {
	f := DefaultFiles()
	md, err := toml.DecodeFile(path, &f)
	if err != nil {
		return Files{}, fmt.Errorf("config: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return Files{}, fmt.Errorf("config: unknown keys: %s", strings.Join(keys, ", "))
	}
	if err := f.Validate(); err != nil {
		return Files{}, err
	}
	return f, nil
}

func (f Files) Validate() error {
	var errs []error
	if err := cleanAbsPath("socket", f.Socket); err != nil {
		errs = append(errs, err)
	}
	if m, err := strconv.ParseUint(f.SocketMode, 8, 32); err != nil || m > 0o777 {
		errs = append(errs, fmt.Errorf("socket_mode: %q must be an octal mode between 0 and 0777", f.SocketMode))
	}
	switch {
	case f.PanelUID < -1:
		errs = append(errs, fmt.Errorf("panel_uid: %d is not a uid (-1 = unset)", f.PanelUID))
	case f.PanelUID == -1 && !f.Dev:
		errs = append(errs, errors.New("panel_uid must be set to the uid of the panel user"))
	}
	if err := cleanAbsPath("instances_dir", f.InstancesDir); err != nil {
		errs = append(errs, err)
	} else if f.InstancesDir == "/" {
		errs = append(errs, errors.New("instances_dir must not be /"))
	}
	if err := checkResticBinary(f.ResticBinary, f.Dev); err != nil {
		errs = append(errs, err)
	}
	l := f.Limits
	for name, v := range map[string]int64{
		"max_upload_bytes": l.MaxUploadBytes, "max_extract_bytes": l.MaxExtractBytes,
		"max_extract_entries": l.MaxExtractEntries, "max_text_bytes": l.MaxTextBytes,
		"max_list_entries": int64(l.MaxListEntries), "max_backup_bytes": l.MaxBackupBytes,
		"max_backup_entries": l.MaxBackupEntries,
	} {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("limits.%s must be > 0", name))
		}
	}
	if l.ReserveBytes < 0 {
		errs = append(errs, errors.New("limits.reserve_bytes must be >= 0"))
	}
	if l.MaxTextBytes > maxTextBytesLimit {
		errs = append(errs, fmt.Errorf("limits.max_text_bytes must be <= %d", maxTextBytesLimit))
	}
	if l.MaxBackupJobs < 1 || l.MaxBackupJobs > maxBackupJobsLimit {
		errs = append(errs, fmt.Errorf("limits.max_backup_jobs must be between 1 and %d", maxBackupJobsLimit))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}

// checkResticBinary: in production a clean absolute path; with dev also a bare command name
// (looked up in PATH by the service).
func checkResticBinary(p string, dev bool) error {
	if dev && p != "" && !strings.ContainsRune(p, '/') && !strings.ContainsRune(p, 0) {
		return nil
	}
	return cleanAbsPath("restic_binary", p)
}
