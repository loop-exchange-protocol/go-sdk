package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

const (
	maxPackageConfigBytes = 64 << 10
	defaultInstallTimeout = 15 * time.Minute
)

// Install resolves an exactly pinned platform-specific Helper from the first
// locally authorized OCI repository that contains it.
func Install(ctx context.Context, kind string, contract spec.Contract, implementation extension.Implementation, repositories []extension.Repository) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultInstallTimeout)
		defer cancel()
	}
	var eligible []extension.Repository
	for _, repository := range repositories {
		if !repository.AutoInstall || !contains(repository.TrustedNamespaces, implementation.Package.Namespace) {
			continue
		}
		eligible = append(eligible, repository)
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("no auto-install repository trusts implementation namespace %q", implementation.Package.Namespace)
	}
	if command, ok := cachedCommand(kind, contract, implementation); ok {
		return command, nil
	}
	var attempted []string
	for _, repository := range eligible {
		attempted = append(attempted, repository.ID)
		command, found, err := installFrom(ctx, repository, kind, contract, implementation)
		if err == nil {
			return command, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if found {
			return nil, fmt.Errorf("repository %s returned an invalid implementation %s: %w", repository.ID, implementation.Package.String(), err)
		}
	}
	return nil, fmt.Errorf("implementation %s was not available from authorized repositories %s", implementation.Package.String(), strings.Join(attempted, ", "))
}

func installFrom(ctx context.Context, repository extension.Repository, kind string, contract spec.Contract, implementation extension.Implementation) ([]string, bool, error) {
	parsed, err := url.Parse(repository.URL)
	if err != nil {
		return nil, false, err
	}
	reference := strings.Trim(parsed.Host+parsed.Path, "/") + "/" + implementation.Package.Namespace + "/" + implementation.Package.Name
	remoteRepository, err := remote.NewRepository(reference)
	if err != nil {
		return nil, false, fmt.Errorf("open OCI repository %s: %w", repository.ID, err)
	}
	remoteRepository.Client = repositoryClient(parsed.Host, repository.Credential)
	manifestDescriptor, manifestBytes, err := oras.FetchBytes(ctx, remoteRepository, implementation.Digest, oras.FetchBytesOptions{MaxBytes: 4 << 20})
	if err != nil {
		return nil, !errors.Is(err, errdef.ErrNotFound), fmt.Errorf("fetch OCI manifest from %s: %w", repository.ID, err)
	}
	if manifestDescriptor.Digest.String() != implementation.Digest {
		return nil, true, fmt.Errorf("OCI manifest digest mismatch")
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, true, fmt.Errorf("decode OCI manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return nil, true, err
	}
	_, descriptorBytes, err := oras.FetchBytes(ctx, remoteRepository.Blobs(), manifest.Config.Digest.String(), oras.FetchBytesOptions{MaxBytes: maxPackageConfigBytes})
	if err != nil {
		return nil, true, fmt.Errorf("fetch Extension Package descriptor: %w", err)
	}
	var descriptor extension.PackageDescriptor
	if err := decodeStrictJSON(descriptorBytes, &descriptor); err != nil {
		return nil, true, fmt.Errorf("decode Extension Package descriptor: %w", err)
	}
	if err := validatePackageDescriptor(descriptor, kind, contract, implementation.Package); err != nil {
		return nil, true, err
	}
	layer := manifest.Layers[0]
	if layer.Size > extension.MaxPackageBinaryBytes {
		return nil, true, fmt.Errorf("Extension Package executable exceeds %d bytes", extension.MaxPackageBinaryBytes)
	}
	_, binary, err := oras.FetchBytes(ctx, remoteRepository.Blobs(), layer.Digest.String(), oras.FetchBytesOptions{MaxBytes: extension.MaxPackageBinaryBytes})
	if err != nil {
		return nil, true, fmt.Errorf("fetch Extension Package executable: %w", err)
	}
	path, err := cachePackage(implementation.Digest, manifestBytes, descriptorBytes, descriptor, binary)
	if err != nil {
		return nil, true, err
	}
	return append([]string{path}, descriptor.Arguments...), true, nil
}

func cachedCommand(kind string, contract spec.Contract, implementation extension.Implementation) ([]string, bool) {
	root, err := extensionCacheRoot()
	if err != nil || !strings.HasPrefix(implementation.Digest, "sha256:") {
		return nil, false
	}
	directory := filepath.Join(root, "sha256", strings.TrimPrefix(implementation.Digest, "sha256:"))
	manifestBytes, err := readRegularFileLimit(filepath.Join(directory, "manifest.json"), 4<<20)
	if err != nil || contentDigest(manifestBytes) != implementation.Digest {
		return nil, false
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || validateManifest(manifest) != nil {
		return nil, false
	}
	descriptorBytes, err := readRegularFileLimit(filepath.Join(directory, "descriptor.json"), maxPackageConfigBytes)
	if err != nil || !matchesDescriptor(manifest.Config, descriptorBytes) {
		return nil, false
	}
	var descriptor extension.PackageDescriptor
	if err := decodeStrictJSON(descriptorBytes, &descriptor); err != nil || validatePackageDescriptor(descriptor, kind, contract, implementation.Package) != nil {
		return nil, false
	}
	executable := filepath.Join(directory, descriptor.Entrypoint)
	if !validCachedExecutable(executable, manifest.Layers[0].Digest.String()) {
		return nil, false
	}
	return append([]string{executable}, descriptor.Arguments...), true
}

func cachePackage(manifestDigest string, manifestBytes, descriptorBytes []byte, descriptor extension.PackageDescriptor, binary []byte) (string, error) {
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || len(manifest.Layers) != 1 {
		return "", fmt.Errorf("cache invalid Extension Package manifest")
	}
	path, err := cacheExecutable(manifestDigest, manifest.Layers[0].Digest.String(), descriptor.Entrypoint, binary)
	if err != nil {
		return "", err
	}
	if err := atomicWriteFile(filepath.Join(filepath.Dir(path), "descriptor.json"), descriptorBytes, 0o600); err != nil {
		return "", err
	}
	if err := atomicWriteFile(filepath.Join(filepath.Dir(path), "manifest.json"), manifestBytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func atomicWriteFile(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".metadata-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func repositoryClient(host, credential string) *auth.Client {
	client := &auth.Client{Client: retry.DefaultClient, Cache: auth.NewCache()}
	if credential != "" {
		if secret := os.Getenv(credential); secret != "" {
			resolved := auth.Credential{AccessToken: secret}
			if username, password, ok := strings.Cut(secret, ":"); ok && username != "" && password != "" {
				resolved = auth.Credential{Username: username, Password: password}
			}
			client.Credential = auth.StaticCredential(host, resolved)
		}
	}
	client.SetUserAgent("lxp/0.1.0-alpha.4")
	return client
}

func validatePackageDescriptor(descriptor extension.PackageDescriptor, kind string, contract, implementation spec.Contract) error {
	if descriptor.APIVersion != spec.APIVersion || descriptor.Kind != "ExtensionPackage" || descriptor.ExtensionKind != kind || descriptor.Contract != contract || descriptor.Implementation != implementation || descriptor.Protocol != extension.HelperProtocol {
		return fmt.Errorf("Extension Package descriptor does not match configured binding")
	}
	if descriptor.OS != runtime.GOOS || descriptor.Architecture != runtime.GOARCH {
		return fmt.Errorf("Extension Package targets %s/%s, current platform is %s/%s", descriptor.OS, descriptor.Architecture, runtime.GOOS, runtime.GOARCH)
	}
	if descriptor.Entrypoint == "" || filepath.Base(descriptor.Entrypoint) != descriptor.Entrypoint || strings.ContainsAny(descriptor.Entrypoint, `/\\`) {
		return fmt.Errorf("Extension Package entrypoint must be one file name")
	}
	for _, argument := range descriptor.Arguments {
		if argument == "" || strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("Extension Package contains an invalid argument")
		}
	}
	return nil
}

func validateManifest(manifest ocispec.Manifest) error {
	if manifest.MediaType != ocispec.MediaTypeImageManifest || manifest.ArtifactType != extension.PackageArtifactType || manifest.Config.MediaType != extension.PackageConfigMediaType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != extension.PackageBinaryMediaType {
		return fmt.Errorf("OCI manifest is not a canonical LXP Extension Package")
	}
	if manifest.Config.Size < 0 || manifest.Config.Size > maxPackageConfigBytes || manifest.Layers[0].Size < 0 || manifest.Layers[0].Size > extension.MaxPackageBinaryBytes {
		return fmt.Errorf("OCI manifest declares an invalid Extension Package size")
	}
	if !validSHA256Digest(manifest.Config.Digest.String()) || !validSHA256Digest(manifest.Layers[0].Digest.String()) {
		return fmt.Errorf("Extension Package config and executable must use SHA-256")
	}
	return nil
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func matchesDescriptor(descriptor ocispec.Descriptor, content []byte) bool {
	return descriptor.Size == int64(len(content)) && descriptor.Digest.String() == contentDigest(content)
}

func contentDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cacheExecutable(manifestDigest, binaryDigest, entrypoint string, binary []byte) (string, error) {
	root, err := extensionCacheRoot()
	if err != nil {
		return "", err
	}
	digestHex := strings.TrimPrefix(manifestDigest, "sha256:")
	destination := filepath.Join(root, "sha256", digestHex, entrypoint)
	if validCachedExecutable(destination, binaryDigest) {
		return destination, nil
	}
	destinationDir := filepath.Dir(destination)
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(destinationDir, ".install-")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(binary); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Chmod(0o700); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", fmt.Errorf("commit Extension Package cache: %w", err)
	}
	if !validCachedExecutable(destination, binaryDigest) {
		return "", fmt.Errorf("cached Extension Package executable failed digest verification")
	}
	return destination, nil
}

func extensionCacheRoot() (string, error) {
	if configured := os.Getenv("LXP_EXTENSION_CACHE"); configured != "" {
		return filepath.Abs(configured)
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate extension cache: %w", err)
	}
	return filepath.Join(root, "lxp", "extensions"), nil
}

func validCachedExecutable(path, expectedDigest string) bool {
	data, err := readRegularFileLimit(path, extension.MaxPackageBinaryBytes)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(data)
	return "sha256:"+hex.EncodeToString(digest[:]) == expectedDigest
}

func readRegularFileLimit(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("cache entry is not a regular file")
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("cache entry exceeds size limit")
	}
	return os.ReadFile(path)
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("expected exactly one JSON value")
		}
		return err
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
