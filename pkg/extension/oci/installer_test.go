package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

func TestValidatePackageDescriptorRequiresExactBindingAndPlatform(t *testing.T) {
	contract := spec.Contract{Namespace: "example.test", Name: "source", Version: "v1"}
	implementation := spec.Contract{Namespace: "example.test", Name: "provider-source", Version: "1.0.0"}
	descriptor := extension.PackageDescriptor{
		APIVersion: spec.APIVersion, Kind: "ExtensionPackage", ExtensionKind: extension.KindProvider,
		Contract: contract, Implementation: implementation, Protocol: extension.HelperProtocol,
		OS: runtime.GOOS, Architecture: runtime.GOARCH, Entrypoint: "lxp-provider-source",
	}
	if err := validatePackageDescriptor(descriptor, extension.KindProvider, contract, implementation); err != nil {
		t.Fatalf("valid descriptor rejected: %v", err)
	}
	descriptor.Implementation.Version = "2.0.0"
	if err := validatePackageDescriptor(descriptor, extension.KindProvider, contract, implementation); err == nil {
		t.Fatal("mismatched implementation unexpectedly accepted")
	}
}

func TestInstallPullsPinnedOCIHelper(t *testing.T) {
	contract := spec.Contract{Namespace: "example.test", Name: "source", Version: "v1"}
	implementation := extension.Implementation{
		Source:  "repository",
		Package: spec.Contract{Namespace: "example.test", Name: "provider-source", Version: "1.0.0"},
	}
	descriptor := extension.PackageDescriptor{
		APIVersion: spec.APIVersion, Kind: "ExtensionPackage", ExtensionKind: extension.KindProvider,
		Contract: contract, Implementation: implementation.Package, Protocol: extension.HelperProtocol,
		OS: runtime.GOOS, Architecture: runtime.GOARCH, Entrypoint: "lxp-provider-source", Arguments: []string{"serve"},
	}
	descriptorBytes, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("platform helper binary")
	configDescriptor := contentDescriptor(extension.PackageConfigMediaType, descriptorBytes)
	binaryDescriptor := contentDescriptor(extension.PackageBinaryMediaType, binary)
	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: extension.PackageArtifactType,
		Config:       configDescriptor,
		Layers:       []ocispec.Descriptor{binaryDescriptor},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestDescriptor := contentDescriptor(ocispec.MediaTypeImageManifest, manifestBytes)
	implementation.Digest = manifestDescriptor.Digest.String()
	blobs := map[string][]byte{
		configDescriptor.Digest.String(): descriptorBytes,
		binaryDescriptor.Digest.String(): binary,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDescriptor.Digest.String())
			w.Header().Set("Content-Length", fmt.Sprint(len(manifestBytes)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(manifestBytes)
			}
		case strings.Contains(r.URL.Path, "/blobs/"):
			key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			// Digests contain a colon, which remains in the final URL segment.
			if strings.Contains(r.URL.RawPath, "%3A") {
				key = strings.ReplaceAll(key, "%3A", ":")
			}
			content, ok := blobs[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", key)
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(content)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	originalClient := retry.DefaultClient
	retry.DefaultClient = server.Client()
	defer func() { retry.DefaultClient = originalClient }()
	t.Setenv("LXP_EXTENSION_CACHE", t.TempDir())
	repositoryURL := "https://" + strings.TrimPrefix(server.URL, "https://") + "/extensions"
	command, err := Install(context.Background(), extension.KindProvider, contract, implementation, []extension.Repository{{
		ID: "test", URL: repositoryURL, AutoInstall: true, TrustedNamespaces: []string{"example.test"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(command) != 2 || command[1] != "serve" {
		t.Fatalf("unexpected Helper command %#v", command)
	}
	installed, err := os.ReadFile(command[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != string(binary) {
		t.Fatalf("unexpected installed binary %q", installed)
	}
	if err := os.WriteFile(command[0], []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), extension.KindProvider, contract, implementation, []extension.Repository{{
		ID: "test", URL: repositoryURL, AutoInstall: true, TrustedNamespaces: []string{"example.test"},
	}}); err != nil {
		t.Fatalf("repair corrupt OCI cache: %v", err)
	}
	if !validCachedExecutable(command[0], binaryDescriptor.Digest.String()) {
		t.Fatal("OCI install did not repair corrupt executable")
	}
	server.Close()
	offlineCommand, err := Install(context.Background(), extension.KindProvider, contract, implementation, []extension.Repository{{
		ID: "test", URL: repositoryURL, AutoInstall: true, TrustedNamespaces: []string{"example.test"},
	}})
	if err != nil {
		t.Fatalf("reuse cached Helper offline: %v", err)
	}
	if offlineCommand[0] != command[0] {
		t.Fatalf("offline cache returned a different executable: %#v", offlineCommand)
	}
}

func contentDescriptor(mediaType string, content []byte) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(content), Size: int64(len(content))}
}

func TestCacheExecutableRepairsCorruption(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("LXP_EXTENSION_CACHE", cache)
	binary := []byte("helper executable")
	digest := sha256.Sum256(binary)
	binaryDigest := "sha256:" + hex.EncodeToString(digest[:])
	manifestDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	path, err := cacheExecutable(manifestDigest, binaryDigest, "lxp-provider-source", binary)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(cache, "sha256", manifestDigest[len("sha256:"):]) {
		t.Fatalf("unexpected cache path %q", path)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheExecutable(manifestDigest, binaryDigest, "lxp-provider-source", binary); err != nil {
		t.Fatal(err)
	}
	if !validCachedExecutable(path, binaryDigest) {
		t.Fatal("corrupt cached executable was not repaired")
	}
}
