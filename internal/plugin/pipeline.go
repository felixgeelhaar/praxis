package plugin

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
)

// Opener loads a plugin artefact from disk and returns its exported
// Plugin symbol. The default implementation (DefaultOpener) wraps Go's
// stdlib `plugin` package and is build-tagged to Linux+macOS only —
// platforms where Go plugin loading is supported. Tests inject a fake
// Opener to exercise the pipeline without producing real .so artefacts.
type Opener interface {
	Open(artefactPath string) (Plugin, error)
}

// PipelineConfig parameters the runtime plugin-load pipeline. Dir,
// Loader and Opener are required; TrustedKeys is required as soon as
// any plugin is discovered (the pipeline fail-closes when it finds a
// plugin with no trust bundle to verify against, even if Strict is
// false).
type PipelineConfig struct {
	Dir         string
	TrustedKeys []*ecdsa.PublicKey
	Loader      Loader
	Opener      Opener
	// LoadHooks is forwarded to LoadWithHooks for every plugin loaded
	// through the pipeline. Optional; nil reduces to the unhookable
	// Load path.
	LoadHooks *LoadHooks
}

// PipelineResult describes the outcome of one Pipeline run. Loaded is
// the set of plugins that successfully reached the registry; Errors
// captures every per-plugin failure so the caller can decide whether
// to log, fail-soft, or abort.
type PipelineResult struct {
	Loaded []Discovered
	Errors []PipelineError
}

// PipelineError pairs a discovered plugin's directory with the reason
// it failed to load. Wraps the underlying error so errors.Is/As works
// against the sentinel set (ErrSignatureMissing, ErrSignatureInvalid,
// ErrNoTrustedKeys, ABIMismatchError, dlopen and Capabilities errors).
type PipelineError struct {
	Dir string
	Err error
}

// Failure-cause labels used by callers to populate the
// praxis_plugin_load_total{result} metric. Stable strings — operators
// build alerts against them.
const (
	ResultSuccess        = "success"
	ResultManifest       = "manifest_invalid"
	ResultArtifact       = "artifact_missing"
	ResultUnsafeArtifact = "unsafe_artifact"
	ResultDuplicate      = "duplicate_name"
	ResultManifestMiss   = "manifest_missing"
	ResultSignature      = "signature_failed"
	ResultNoTrustedKeys  = "no_trusted_keys"
	ResultABIMismatch    = "abi_mismatch"
	ResultDlopen         = "dlopen_failed"
	ResultLoad           = "load_failed"
	ResultCrashed        = "crashed" // post-load: child process or IPC stream died
)

// ClassifyError maps a per-plugin error to one of the Result* constants.
// Falls back to ResultLoad for unrecognised errors so the metric always
// labels something.
func ClassifyError(err error) string {
	switch {
	case err == nil:
		return ResultSuccess
	case errors.Is(err, ErrSignatureInvalid):
		return ResultSignature
	case errors.Is(err, ErrSignatureMissing):
		return ResultSignature
	case errors.Is(err, ErrNoTrustedKeys):
		return ResultNoTrustedKeys
	case errors.Is(err, ErrManifestMissing):
		return ResultManifestMiss
	case errors.Is(err, ErrManifestInvalid):
		return ResultManifest
	case errors.Is(err, ErrArtifactMissing):
		return ResultArtifact
	case errors.Is(err, ErrUnsafeArtifact):
		return ResultUnsafeArtifact
	case errors.Is(err, ErrDuplicateName):
		return ResultDuplicate
	}
	var mm *ABIMismatchError
	if errors.As(err, &mm) {
		return ResultABIMismatch
	}
	// dlopen errors come from the Opener and have no exported sentinel;
	// the message starts with "open plugin" by convention in pipeline.go.
	if msg := err.Error(); len(msg) > 0 && (msg == "open plugin" ||
		(len(msg) > 11 && msg[:11] == "open plugin")) {
		return ResultDlopen
	}
	return ResultLoad
}

// Error implements error.
func (e *PipelineError) Error() string {
	return fmt.Sprintf("plugin %s: %v", e.Dir, e.Err)
}

// Unwrap exposes the underlying cause.
func (e *PipelineError) Unwrap() error { return e.Err }

// RunPipeline executes the discover → verify → open → Load chain over
// every subdirectory of cfg.Dir. Per-plugin failures populate the
// returned PipelineResult.Errors and never stop the sweep — one bad
// plugin must not hide its healthy siblings. The function returns a
// hard error only when the operator-level setup is wrong: the plugin
// directory is missing, or filesystem walk fails.
//
// Empty cfg.Dir is a no-op (plugin discovery disabled).
func RunPipeline(ctx context.Context, cfg PipelineConfig) (PipelineResult, error) {
	if cfg.Dir == "" {
		return PipelineResult{}, nil
	}
	if cfg.Loader == nil {
		return PipelineResult{}, errors.New("plugin pipeline: Loader is required")
	}
	if cfg.Opener == nil {
		return PipelineResult{}, errors.New("plugin pipeline: Opener is required")
	}

	disc, err := Discover(cfg.Dir)
	if err != nil {
		return PipelineResult{}, fmt.Errorf("plugin discovery: %w", err)
	}

	res := PipelineResult{
		Errors: make([]PipelineError, 0, len(disc.Errors)),
	}
	for _, de := range disc.Errors {
		res.Errors = append(res.Errors, PipelineError(de))
	}

	for _, d := range disc.Plugins {
		if err := loadOne(ctx, cfg, d); err != nil {
			res.Errors = append(res.Errors, PipelineError{Dir: d.Dir, Err: err})
			continue
		}
		res.Loaded = append(res.Loaded, d)
	}
	return res, nil
}

func loadOne(ctx context.Context, cfg PipelineConfig, d Discovered) error {
	if err := VerifyDiscovered(d, cfg.TrustedKeys); err != nil {
		return err
	}
	p, err := cfg.Opener.Open(d.Artifact)
	if err != nil {
		return fmt.Errorf("open plugin: %w", err)
	}
	if err := LoadWithHooks(ctx, p, cfg.Loader, cfg.LoadHooks); err != nil {
		return err
	}
	return nil
}
