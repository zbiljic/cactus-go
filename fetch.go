package cactus

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	// EngineVersion is the Cactus engine version, which runs Needle 3 (the
	// default generation), Whistle and Mirage. Needle 2 keeps its own engine; see
	// EngineVersionFor.
	EngineVersion = "4.1.1"

	maxArtifactSize = 64 << 20
)

// LibraryPathEnv returns the environment variables that override engine
// discovery for a generation (zero means 3), in lookup order. The first
// non-empty variable selects the library.
func LibraryPathEnv(generation int) ([]string, error) {
	release, err := releaseFor(generation)
	if err != nil {
		return nil, err
	}
	if release.generation == 2 {
		return []string{"NEEDLE2_LIB_PATH", "NEEDLE_LIB_PATH"}, nil
	}
	return []string{"CACTUS_LIB_PATH"}, nil
}

// Platform identifies a downloadable desktop engine build.
type Platform string

const (
	PlatformDarwinAMD64    Platform = "darwin-amd64"
	PlatformDarwinARM64    Platform = "darwin-arm64"
	PlatformLinuxAMD64     Platform = "linux-amd64"
	PlatformLinuxARM64     Platform = "linux-arm64"
	PlatformLinuxAMD64Musl Platform = "linux-amd64-musl"
	PlatformLinuxARM64Musl Platform = "linux-arm64-musl"
	PlatformWindowsAMD64   Platform = "windows-amd64"
	PlatformWindowsARM64   Platform = "windows-arm64"
)

// FetchOptions configures an engine download. An empty Platform selects the
// current process platform. CacheDir is the exact destination directory.
type FetchOptions struct {
	// Generation selects 2 or 3; zero defaults to 3.
	Generation int
	Platform   Platform
	CacheDir   string
	Client     *http.Client
}

// engineArtifact pins one file published on Hugging Face at a fixed commit.
type engineArtifact struct {
	repo, revision, path string
	size                 int64
	checksum             string
	// archivePath is the library inside a wheel; empty for weights.
	archivePath string
	// libraryName is the installed file name in the cache directory.
	libraryName string
}

func needle2Wheel(tag, library string, size int64, checksum string) engineArtifact {
	return engineArtifact{
		repo:        "Cactus-Compute/needle2",
		revision:    "32e9e3a93b205f786929697446ae669cf0a84579",
		path:        "python/cactus_needle-2.0.4-py3-none-" + tag + ".whl",
		size:        size,
		checksum:    checksum,
		archivePath: "needle/" + library,
		libraryName: library,
	}
}

var artifactsV2 = map[Platform]engineArtifact{
	PlatformDarwinARM64: needle2Wheel("macosx_11_0_arm64", "libneedle.dylib", 13323841,
		"abae4cca0a4d84ec73da4bde18803b9be812a9209e48fb7fa372002ebaa60265"),
	PlatformDarwinAMD64: needle2Wheel("macosx_11_0_x86_64", "libneedle.dylib", 13217806,
		"071e93d996021b4f6b5bee055777cd81be9f6963d2565115ec461b9f71c7245e"),
	PlatformLinuxARM64: needle2Wheel("manylinux2014_aarch64", "libneedle.so", 13290005,
		"e655a13f9d3239e601936ff2d0f6acafd8f6020aaf7b1862ce4fceefacfd6556"),
	PlatformLinuxAMD64: needle2Wheel("manylinux2014_x86_64", "libneedle.so", 13283977,
		"13a84e6c73095fd175b11d46a30a984b62123d94421b769c107074aff7f65c2b"),
	PlatformLinuxARM64Musl: needle2Wheel("musllinux_1_2_aarch64", "libneedle.so", 13292645,
		"eaacaf7925e596fd7d18814bece02770b250039c0b92bbdd899967beb012c114"),
	PlatformLinuxAMD64Musl: needle2Wheel("musllinux_1_2_x86_64", "libneedle.so", 13286334,
		"f94633df433643a3b9b20b84ea19f12b423809d112ba2421d87aad8b07ca3277"),
	PlatformWindowsAMD64: needle2Wheel("win_amd64", "libneedle.dll", 13300371,
		"b4803501a109af3782efe112be27515947c108ecec3bf85703ae6b97bb7210e1"),
	PlatformWindowsARM64: needle2Wheel("win_arm64", "libneedle.dll", 13269152,
		"e03d1301f37dccd468a9b6619c31f82e2c428465ea7c055bf21214feff9b7869"),
}

// cactusWheel pins a runtime wheel from the Cactus engine release. Sizes and
// checksums come from releases/4.1.1/manifest.json (sha256
// 27168631232dbff52a205b54bbfad54fd9b81f06fa368657d07449e2221f5e25).
func cactusWheel(tag, library string, size int64, checksum string) engineArtifact {
	return engineArtifact{
		repo:     "Cactus-Compute/cactus",
		revision: "d2c5cd70432ed78345f3ba6b620bff2feeec6c86",
		path: "releases/" + EngineVersion + "/python/cactus_runtime-" + EngineVersion +
			"-py3-none-" + tag + ".whl",
		size:        size,
		checksum:    checksum,
		archivePath: "cactus/" + library,
		libraryName: library,
	}
}

var artifacts = map[Platform]engineArtifact{
	PlatformDarwinARM64: cactusWheel("macosx_11_0_arm64", "libcactus.dylib", 722805,
		"16ce3c02e87db1ef3ab3974d5d31ff0680bb1004cd49b3606d5cfe4db8b9b536"),
	PlatformDarwinAMD64: cactusWheel("macosx_11_0_x86_64", "libcactus.dylib", 781063,
		"a1c382375b6df5f1f57f075e48c5fcd9da84b666a2c97bd4b49c10543b8b905f"),
	PlatformLinuxARM64: cactusWheel("manylinux2014_aarch64", "libcactus.so", 822733,
		"5b67ab4a5b44ee846e7662940e2dd854006ec8766c63c81405338428576e914d"),
	PlatformLinuxAMD64: cactusWheel("manylinux2014_x86_64", "libcactus.so", 856749,
		"b48fe924d108f84bbe265539920b1b2b98daf936e22af461408f1856a41546d6"),
	PlatformLinuxARM64Musl: cactusWheel("musllinux_1_2_aarch64", "libcactus.so", 822507,
		"d2cba6fba4cca494298c75259a881da62bcac543b0da8fddf92678566d811a2e"),
	PlatformLinuxAMD64Musl: cactusWheel("musllinux_1_2_x86_64", "libcactus.so", 857831,
		"ae4f0f9d7245f37da155816e8ef62725dea905dc3c47664416105fba7dd3698f"),
	PlatformWindowsAMD64: cactusWheel("win_amd64", "libcactus.dll", 865905,
		"876eea21e31bcad795b9e84cfaadf65e01a32a64ef113c8b77616d33ab8e3169"),
	PlatformWindowsARM64: cactusWheel("win_arm64", "libcactus.dll", 785327,
		"a5cebd1cac7cb5e54f0405a2c6e5f492e5c103fd2cc9895a766583575c3c70ce"),
}

var baseWeights = engineArtifact{
	repo:        "Cactus-Compute/needle3",
	revision:    "d6c7d6a2e9655505e0f8e23339356dd1705900eb",
	path:        "needle3.cact",
	size:        35335380,
	checksum:    "c9d915eca282ed42d1a09b143b592adb4cc6744ffe2d294adf5cfc5548170c38",
	libraryName: "needle3.cact",
}

var speechWeights = engineArtifact{
	repo:        "Cactus-Compute/whistle",
	revision:    "741118687e1f008f06d1351a402a731274789790",
	path:        "whistle.cact",
	size:        16919407,
	checksum:    "b6e02f048568ac5d01a2042556c658061e699acbc0aa2a1439f52f3d461dffeb",
	libraryName: "whistle.cact",
}

var visionWeights = engineArtifact{
	repo:        "Cactus-Compute/mirage",
	revision:    "76c1e9e53381aca8fc3843c636216c96ff220122",
	path:        "mirage.cact",
	size:        21611043,
	checksum:    "4249a0f7515272ff76b9414bec17d7e7daf84b15b66c7fb4f17fce1e66f9f237",
	libraryName: "mirage.cact",
}

// engineRelease pins one downloadable release: the engine wheels per platform
// and, when the engine needs them, the base weights it loads.
type engineRelease struct {
	generation int
	version    string
	artifacts  map[Platform]engineArtifact
	// weights is the base archive loaded with cactus_load; empty for Needle 2.
	weights engineArtifact
}

var (
	releaseV2 = engineRelease{
		generation: 2,
		version:    "2.0.4",
		artifacts:  artifactsV2,
	}
	releaseV3 = engineRelease{
		generation: 3,
		version:    EngineVersion,
		artifacts:  artifacts,
		weights:    baseWeights,
	}
	// whistleRelease pins the speech weights, which run on the Cactus engine
	// and share its cache directory.
	whistleRelease = engineRelease{
		generation: 3,
		version:    EngineVersion,
		artifacts:  artifacts,
		weights:    speechWeights,
	}
	// mirageRelease pins the vision weights, which also share the Cactus
	// engine cache directory.
	mirageRelease = engineRelease{
		generation: 3,
		version:    EngineVersion,
		artifacts:  artifacts,
		weights:    visionWeights,
	}
)

func releaseFor(generation int) (engineRelease, error) {
	switch generation {
	case 2:
		return releaseV2, nil
	case 0, 3:
		return releaseV3, nil
	default:
		return engineRelease{}, fmt.Errorf("%w %d; want 2 or 3", ErrUnsupportedGeneration, generation)
	}
}

// EngineVersionFor returns the pinned engine version for a generation (zero means 3).
func EngineVersionFor(generation int) (string, error) {
	release, err := releaseFor(generation)
	return release.version, err
}

var fetchMu sync.Mutex

// SupportedPlatforms returns the desktop engine builds known to this version.
func SupportedPlatforms() []Platform {
	return []Platform{
		PlatformDarwinAMD64,
		PlatformDarwinARM64,
		PlatformLinuxAMD64,
		PlatformLinuxARM64,
		PlatformLinuxAMD64Musl,
		PlatformLinuxARM64Musl,
		PlatformWindowsAMD64,
		PlatformWindowsARM64,
	}
}

// CurrentPlatform returns the engine build matching the running process.
func CurrentPlatform() (Platform, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/amd64":
		return PlatformDarwinAMD64, nil
	case "darwin/arm64":
		return PlatformDarwinARM64, nil
	case "linux/amd64":
		if isMusl() {
			return PlatformLinuxAMD64Musl, nil
		}
		return PlatformLinuxAMD64, nil
	case "linux/arm64":
		if isMusl() {
			return PlatformLinuxARM64Musl, nil
		}
		return PlatformLinuxARM64, nil
	case "windows/amd64":
		return PlatformWindowsAMD64, nil
	case "windows/arm64":
		return PlatformWindowsARM64, nil
	default:
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	}
}

// CachedEngine returns the expected cached library path without downloading
// anything. It accepts libraries installed by cactus-go or another client.
func CachedEngine(options FetchOptions) (string, error) {
	release, err := releaseFor(options.Generation)
	if err != nil {
		return "", err
	}
	platform := options.Platform
	if platform == "" {
		var err error
		platform, err = CurrentPlatform()
		if err != nil {
			return "", err
		}
	}
	artifact, ok := release.artifacts[platform]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedPlatform, platform)
	}
	cacheDir := options.CacheDir
	if cacheDir == "" {
		var err error
		cacheDir, err = defaultCacheDir(release, platform)
		if err != nil {
			return "", err
		}
	}
	path := filepath.Join(cacheDir, artifact.libraryName)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrEngineNotFound, path)
		}
		return "", fmt.Errorf("cactus: inspect cached engine: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a regular file", ErrEngineNotFound, path)
	}
	return path, nil
}

// FetchEngine downloads, verifies, and caches the shared library and required
// base weights for one desktop platform. It returns the library path.
func FetchEngine(ctx context.Context, options FetchOptions) (string, error) {
	path, err := fetchEngineLibrary(ctx, options)
	if err != nil {
		return "", err
	}
	if _, err := fetchBaseWeights(ctx, options); err != nil {
		return "", err
	}
	return path, nil
}

// FetchWhistle caches the Cactus engine and Whistle speech weights for offline
// use. It returns the library path and does not download text weights.
// Generation must be zero or 3; Platform and CacheDir follow FetchEngine.
func FetchWhistle(ctx context.Context, options FetchOptions) (string, error) {
	if options.Generation != 0 && options.Generation != 3 {
		return "", fmt.Errorf("%w: Whistle requires generation 3", ErrUnsupportedGeneration)
	}
	path, err := fetchEngineLibrary(ctx, options)
	if err != nil {
		return "", err
	}
	if _, err := fetchSpeechWeights(ctx, options); err != nil {
		return "", err
	}
	return path, nil
}

// FetchMirage caches the Cactus engine and Mirage vision weights for offline
// use. It returns the library path and does not download text or speech
// weights. Generation must be zero or 3; Platform and CacheDir follow
// FetchEngine.
func FetchMirage(ctx context.Context, options FetchOptions) (string, error) {
	if options.Generation != 0 && options.Generation != 3 {
		return "", fmt.Errorf("%w: Mirage requires generation 3", ErrUnsupportedGeneration)
	}
	path, err := fetchEngineLibrary(ctx, options)
	if err != nil {
		return "", err
	}
	if _, err := fetchVisionWeights(ctx, options); err != nil {
		return "", err
	}
	return path, nil
}

func fetchVisionWeights(ctx context.Context, options FetchOptions) (string, error) {
	return fetchWithOptions(ctx, options, mirageRelease, mirageRelease.weights)
}

func fetchSpeechWeights(ctx context.Context, options FetchOptions) (string, error) {
	return fetchWithOptions(ctx, options, whistleRelease, whistleRelease.weights)
}

func fetchEngineLibrary(ctx context.Context, options FetchOptions) (string, error) {
	release, err := releaseFor(options.Generation)
	if err != nil {
		return "", err
	}
	platform := options.Platform
	if platform == "" {
		var err error
		platform, err = CurrentPlatform()
		if err != nil {
			return "", err
		}
	}
	artifact, ok := release.artifacts[platform]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedPlatform, platform)
	}
	return fetchWithOptions(ctx, options, release, artifact)
}

// fetchBaseWeights returns the base archive for the selected generation, or an
// empty path when the generation embeds its model in the engine.
func fetchBaseWeights(ctx context.Context, options FetchOptions) (string, error) {
	release, err := releaseFor(options.Generation)
	if err != nil {
		return "", err
	}
	if release.weights.path == "" {
		return "", nil
	}
	return fetchWithOptions(ctx, options, release, release.weights)
}

func fetchWithOptions(
	ctx context.Context,
	options FetchOptions,
	release engineRelease,
	artifact engineArtifact,
) (string, error) {
	cacheDir := options.CacheDir
	if cacheDir == "" {
		var err error
		cacheDir, err = defaultCacheDir(release, options.Platform)
		if err != nil {
			return "", err
		}
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	fetchMu.Lock()
	defer fetchMu.Unlock()
	return fetchArtifact(ctx, client, cacheDir, artifact, artifactURL(artifact))
}

func fetchArtifact(
	ctx context.Context,
	client *http.Client,
	cacheDir string,
	artifact engineArtifact,
	url string,
) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("cactus: create engine cache: %w", err)
	}
	target := filepath.Join(cacheDir, artifact.libraryName)
	marker := target + ".sha256"
	if cachedArtifact(target, marker, artifact.checksum) {
		return target, nil
	}

	wheel, err := os.CreateTemp(cacheDir, ".cactus-*.whl")
	if err != nil {
		return "", fmt.Errorf("cactus: create temporary download: %w", err)
	}
	wheelPath := wheel.Name()
	if err := wheel.Close(); err != nil {
		return "", fmt.Errorf("cactus: close temporary download: %w", err)
	}
	defer os.Remove(wheelPath)

	if err := downloadArtifact(ctx, client, url, wheelPath, artifact.size, artifact.checksum); err != nil {
		return "", err
	}
	if artifact.archivePath == "" {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("cactus: replace cached weights: %w", err)
		}
		if err := os.Rename(wheelPath, target); err != nil {
			return "", fmt.Errorf("cactus: install weights: %w", err)
		}
	} else {
		if err := extractLibrary(wheelPath, cacheDir, target, artifact.archivePath); err != nil {
			return "", err
		}
	}
	installedHash, err := installedChecksum(target)
	if err != nil {
		return "", fmt.Errorf("cactus: hash installed artifact: %w", err)
	}
	if err := os.WriteFile(marker, []byte(artifact.checksum+"\n"+installedHash+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("cactus: write engine checksum marker: %w", err)
	}
	return target, nil
}

func downloadArtifact(ctx context.Context, client *http.Client, url, destination string, size int64, checksum string) error {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			delay := time.Duration(1<<(attempt-1)) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		lastErr = downloadAttempt(ctx, client, url, destination, size, checksum)
		if lastErr == nil {
			return nil
		}
		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}
	}
	return fmt.Errorf("cactus: download artifact after 3 attempts: %w", lastErr)
}

func downloadAttempt(ctx context.Context, client *http.Client, url, destination string, size int64, checksum string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "cactus-go/"+EngineVersion)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("download returned %s", response.Status)
	}
	if response.ContentLength >= 0 && response.ContentLength != size {
		return fmt.Errorf("size mismatch: server reports %d bytes, want %d", response.ContentLength, size)
	}

	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != size {
		return fmt.Errorf("size mismatch: got %d bytes, want %d", written, size)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != checksum {
		return fmt.Errorf("checksum mismatch: got %s, want %s", actual, checksum)
	}
	return nil
}

func extractLibrary(wheelPath, cacheDir, target, archivePath string) error {
	archive, err := zip.OpenReader(wheelPath)
	if err != nil {
		return fmt.Errorf("cactus: open engine archive: %w", err)
	}
	defer archive.Close()

	var source *zip.File
	for _, file := range archive.File {
		if file.Name == archivePath {
			source = file
			break
		}
	}
	if source == nil {
		return fmt.Errorf("cactus: engine archive does not contain %s", archivePath)
	}
	reader, err := source.Open()
	if err != nil {
		return fmt.Errorf("cactus: open engine library: %w", err)
	}
	defer reader.Close()

	temporary, err := os.CreateTemp(cacheDir, ".library-*")
	if err != nil {
		return fmt.Errorf("cactus: create temporary library: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	written, copyErr := io.Copy(temporary, io.LimitReader(reader, maxArtifactSize+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("cactus: extract engine library: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("cactus: close engine library: %w", closeErr)
	}
	if written > maxArtifactSize {
		return fmt.Errorf("cactus: engine library exceeds %d bytes", maxArtifactSize)
	}
	if err := os.Chmod(temporaryPath, 0o755); err != nil {
		return fmt.Errorf("cactus: set engine library permissions: %w", err)
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cactus: replace cached engine: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("cactus: install engine library: %w", err)
	}
	return nil
}

func cachedArtifact(target, marker, checksum string) bool {
	installedHash, err := installedChecksum(target)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(marker)
	return err == nil && string(data) == checksum+"\n"+installedHash+"\n"
}

func installedChecksum(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxArtifactSize {
		return "", errors.New("invalid cached artifact size or type")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxArtifactSize+1))
	if err != nil {
		return "", err
	}
	if written == 0 || written > maxArtifactSize {
		return "", errors.New("invalid cached artifact size")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func artifactURL(artifact engineArtifact) string {
	return fmt.Sprintf(
		"https://huggingface.co/%s/resolve/%s/%s?download=true",
		artifact.repo,
		artifact.revision,
		artifact.path,
	)
}

func defaultCacheDir(release engineRelease, platform Platform) (string, error) {
	if platform == "" {
		var err error
		platform, err = CurrentPlatform()
		if err != nil {
			return "", err
		}
	}
	if _, ok := release.artifacts[platform]; !ok {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedPlatform, platform)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cactus: find home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "cactus-go", release.version, string(platform)), nil
}

func isMusl() bool {
	if data, err := os.ReadFile("/proc/self/maps"); err == nil && bytes.Contains(data, []byte("musl")) {
		return true
	}
	for _, pattern := range []string{"/lib/ld-musl-*.so.1", "/usr/lib/ld-musl-*.so.1"} {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			return true
		}
	}
	return false
}
