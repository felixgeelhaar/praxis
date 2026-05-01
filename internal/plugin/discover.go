package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestFilename is the fixed filename Praxis looks for in each plugin
// directory under PRAXIS_PLUGIN_DIR.
const ManifestFilename = "manifest.json"

// DiskManifest is the on-disk manifest schema. It extends the in-process
// Manifest with deployment metadata: the ABI version the plugin was built
// against and the artefact filename relative to the plugin directory.
type DiskManifest struct {
	Manifest
	ABI      string `json:"abi"`
	Artifact string `json:"artifact"`
}

// Discovered is one validated plugin found on disk. The runtime consumes
// this list at startup, verifies signatures (follow-up task), then loads
// each artefact and calls plugin.Load.
type Discovered struct {
	Dir      string   // absolute path to the plugin directory
	Manifest Manifest // identity + provenance subset of the disk manifest
	ABI      string   // declared ABI version (validated against ABIVersion at load time)
	Artifact string   // absolute path to the plugin artefact file
}

// DiscoveryError carries a per-plugin failure surfaced by Discover. The
// scan continues past these so a single bad plugin cannot hide healthy
// siblings.
type DiscoveryError struct {
	Dir string
	Err error
}

// Error implements error.
func (e *DiscoveryError) Error() string {
	return fmt.Sprintf("plugin %s: %v", e.Dir, e.Err)
}

// Unwrap exposes the underlying cause to errors.Is/As.
func (e *DiscoveryError) Unwrap() error { return e.Err }

// DiscoveryResult separates clean plugins from per-plugin errors so
// callers can decide whether to fail-closed (any error aborts startup)
// or fail-open (log errors, register healthy plugins).
type DiscoveryResult struct {
	Plugins []Discovered
	Errors  []DiscoveryError
}

// Sentinel errors. Callers use errors.Is to branch on the failure mode.
var (
	ErrManifestMissing = errors.New("manifest.json not found")
	ErrManifestInvalid = errors.New("manifest.json invalid")
	ErrUnsafeArtifact  = errors.New("artifact path escapes plugin directory")
	ErrArtifactMissing = errors.New("artifact file not found")
	ErrDuplicateName   = errors.New("duplicate plugin name")
)

// Discover scans root for plugin directories. Each immediate subdirectory
// of root is treated as a candidate plugin: it must contain manifest.json
// declaring name, version, abi, and artifact. Files in root and other
// non-directory entries are ignored silently. Subdirectories without a
// manifest.json surface as ErrManifestMissing.
//
// Per-plugin failures populate Errors and the scan continues. The first
// successfully discovered plugin with a given name wins; later duplicates
// surface as ErrDuplicateName. Plugins are returned sorted by name for
// deterministic startup behaviour.
func Discover(root string) (DiscoveryResult, error) {
	info, err := os.Stat(root)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("plugin discovery: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return DiscoveryResult{}, fmt.Errorf("plugin discovery: %s is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("plugin discovery: read %s: %w", root, err)
	}

	var (
		plugins []Discovered
		errs    []DiscoveryError
		seen    = map[string]string{} // name -> dir of first occurrence
	)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue // README.md, .DS_Store, etc.
		}
		dir := filepath.Join(root, entry.Name())
		d, derr := loadManifest(dir)
		if derr != nil {
			errs = append(errs, DiscoveryError{Dir: dir, Err: derr})
			continue
		}
		if prev, dup := seen[d.Manifest.Name]; dup {
			errs = append(errs, DiscoveryError{
				Dir: dir,
				Err: fmt.Errorf("%w: %q already loaded from %s", ErrDuplicateName, d.Manifest.Name, prev),
			})
			continue
		}
		seen[d.Manifest.Name] = dir
		plugins = append(plugins, d)
	}

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Manifest.Name < plugins[j].Manifest.Name
	})

	return DiscoveryResult{Plugins: plugins, Errors: errs}, nil
}

// loadManifest reads, parses, and validates one plugin directory.
func loadManifest(dir string) (Discovered, error) {
	manifestPath := filepath.Join(dir, ManifestFilename)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Discovered{}, ErrManifestMissing
		}
		return Discovered{}, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}

	var dm DiskManifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dm); err != nil {
		return Discovered{}, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}

	if err := validateManifest(dm); err != nil {
		return Discovered{}, err
	}

	artifactPath, err := resolveArtifact(dir, dm.Artifact)
	if err != nil {
		return Discovered{}, err
	}
	if _, err := os.Stat(artifactPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Discovered{}, fmt.Errorf("%w: %s", ErrArtifactMissing, dm.Artifact)
		}
		return Discovered{}, fmt.Errorf("%w: %v", ErrArtifactMissing, err)
	}

	return Discovered{
		Dir:      dir,
		Manifest: dm.Manifest,
		ABI:      dm.ABI,
		Artifact: artifactPath,
	}, nil
}

// validateManifest enforces the required-fields contract. ABI mismatch is
// not a discovery-time failure: discovery surfaces the declared ABI and
// the runtime decides at load time whether to load it. This keeps the
// concerns separated and lets operators see "plugin X declares ABI v0"
// in the audit log even when the runtime refuses to load it.
func validateManifest(dm DiskManifest) error {
	missing := []string{}
	if strings.TrimSpace(dm.Name) == "" {
		missing = append(missing, "name")
	}
	if strings.TrimSpace(dm.Version) == "" {
		missing = append(missing, "version")
	}
	if strings.TrimSpace(dm.ABI) == "" {
		missing = append(missing, "abi")
	}
	if strings.TrimSpace(dm.Artifact) == "" {
		missing = append(missing, "artifact")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing fields %s", ErrManifestInvalid, strings.Join(missing, ", "))
	}
	return nil
}

// resolveArtifact joins the plugin directory and the declared artefact
// path while rejecting any path that escapes the directory. Absolute
// paths and path-traversal sequences (..) both fail.
func resolveArtifact(dir, artifact string) (string, error) {
	if filepath.IsAbs(artifact) {
		return "", fmt.Errorf("%w: absolute path %q", ErrUnsafeArtifact, artifact)
	}
	cleaned := filepath.Clean(artifact)
	// filepath.Clean preserves a leading ".." which signals escape.
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q escapes plugin directory", ErrUnsafeArtifact, artifact)
	}
	full := filepath.Join(dir, cleaned)
	// Defence in depth: confirm the resolved path is rooted in dir.
	rel, err := filepath.Rel(dir, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: %q escapes plugin directory", ErrUnsafeArtifact, artifact)
	}
	return full, nil
}
