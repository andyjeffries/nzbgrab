.PHONY: build install clean test release release-dry bump-patch bump-minor bump-major \
        dist dist-darwin dist-linux dist-windows checksums \
        aur-publish homebrew-bump deb help

# Version is read from version.txt
VERSION := $(shell cat version.txt 2>/dev/null || echo "0.0.0")
BINARY  := nzbgrab
PKG     := ./cmd/nzbgrab
LDFLAGS := -s -w -X main.version=$(VERSION)

# GitHub repo
GITHUB_USER := andyjeffries
GITHUB_REPO := nzbgrab

# Branch to push releases from (defaults to the current branch)
MAIN_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo master)

# Build output
DIST_DIR := dist

help:
	@echo "Build targets:"
	@echo "  build          - Build binary for current platform"
	@echo "  install        - Install to /usr/local/bin"
	@echo "  test           - Run tests"
	@echo "  clean          - Remove build artifacts"
	@echo ""
	@echo "Release targets:"
	@echo "  bump-patch     - Bump patch version (0.0.X)"
	@echo "  bump-minor     - Bump minor version (0.X.0)"
	@echo "  bump-major     - Bump major version (X.0.0)"
	@echo "  release-dry    - Show what release would do"
	@echo "  release        - Tag and push release (runs dist, creates GitHub release)"
	@echo ""
	@echo "Distribution targets:"
	@echo "  dist           - Build binaries for all platforms"
	@echo "  checksums      - Generate SHA256 checksums"
	@echo ""
	@echo "Package targets:"
	@echo "  aur-publish    - Update and publish AUR package"
	@echo "  homebrew-bump  - Update Homebrew formula"
	@echo "  deb            - Build .deb package"
	@echo ""
	@echo "Current version: $(VERSION)"

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

install: build
	sudo install -Dm755 $(BINARY) /usr/local/bin/$(BINARY)

test:
	go test ./...

clean:
	rm -rf $(BINARY) $(DIST_DIR)

# Version bumping
bump-patch:
	@echo $(VERSION) | awk -F. '{print $$1"."$$2"."$$3+1}' > version.txt
	@echo "Version bumped to $$(cat version.txt)"

bump-minor:
	@echo $(VERSION) | awk -F. '{print $$1"."$$2+1".0"}' > version.txt
	@echo "Version bumped to $$(cat version.txt)"

bump-major:
	@echo $(VERSION) | awk -F. '{print $$1+1".0.0"}' > version.txt
	@echo "Version bumped to $$(cat version.txt)"

# Distribution builds
dist: clean dist-darwin dist-linux dist-windows checksums
	@echo "Built all distributions in $(DIST_DIR)/"

dist-darwin:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-darwin-amd64 $(PKG)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-darwin-arm64 $(PKG)

dist-linux:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-arm64 $(PKG)

dist-windows:
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe $(PKG)

checksums:
	@cd $(DIST_DIR) && sha256sum * > SHA256SUMS

# Release process
release-dry:
	@echo "Would release version $(VERSION)"
	@echo "Steps:"
	@echo "  1. Run tests"
	@echo "  2. Build distributions"
	@echo "  3. Create git tag v$(VERSION)"
	@echo "  4. Push tag to origin"
	@echo "  5. Create GitHub release with binaries"
	@echo ""
	@echo "Run 'make release' to execute"

release: test dist
	@if git rev-parse "v$(VERSION)" >/dev/null 2>&1; then \
		echo "Error: Tag v$(VERSION) already exists"; \
		exit 1; \
	fi
	git add version.txt
	git commit -m "Release v$(VERSION)" || true
	git tag -a "v$(VERSION)" -m "Release v$(VERSION)"
	git push origin $(MAIN_BRANCH)
	git push origin "v$(VERSION)"
	gh release create "v$(VERSION)" $(DIST_DIR)/* \
		--title "v$(VERSION)" \
		--generate-notes
	@echo ""
	@echo "Release v$(VERSION) created!"
	@echo "Next steps:"
	@echo "  make aur-publish     - Update AUR package"
	@echo "  make homebrew-bump   - Update Homebrew formula"

# AUR publishing
aur-publish:
	@echo "Updating AUR package..."
	@if [ ! -d "../nzbgrab-aur" ]; then \
		echo "Cloning AUR repo..."; \
		git clone ssh://aur@aur.archlinux.org/nzbgrab.git ../nzbgrab-aur; \
	fi
	@cd ../nzbgrab-aur && \
		sed -i "s/^pkgver=.*/pkgver=$(VERSION)/" PKGBUILD && \
		sed -i "s/^pkgrel=.*/pkgrel=1/" PKGBUILD && \
		updpkgsums && \
		makepkg --printsrcinfo > .SRCINFO && \
		git add PKGBUILD .SRCINFO && \
		git commit -m "Update to $(VERSION)" && \
		git push
	@echo "AUR package updated to $(VERSION)"

# Homebrew formula update
homebrew-bump:
	@echo "Updating Homebrew formula..."
	@if [ ! -d "../homebrew-tap" ]; then \
		echo "Error: ../homebrew-tap not found"; \
		echo "Clone your tap repo: git clone git@github.com:$(GITHUB_USER)/homebrew-tap.git ../homebrew-tap"; \
		exit 1; \
	fi
	@URL="https://github.com/$(GITHUB_USER)/$(GITHUB_REPO)/archive/refs/tags/v$(VERSION).tar.gz" && \
	SHA=$$(curl -sL "$$URL" | sha256sum | cut -d' ' -f1) && \
	sed -i "s|url \".*\"|url \"$$URL\"|" ../homebrew-tap/Formula/nzbgrab.rb && \
	sed -i "s/sha256 \".*\"/sha256 \"$$SHA\"/" ../homebrew-tap/Formula/nzbgrab.rb && \
	cd ../homebrew-tap && \
		git add Formula/nzbgrab.rb && \
		git commit -m "nzbgrab $(VERSION)" && \
		git push
	@echo "Homebrew formula updated to $(VERSION)"

# Debian package
deb: build
	@mkdir -p $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN
	@mkdir -p $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/usr/bin
	@mkdir -p $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/usr/share/doc/$(BINARY)
	@cp $(BINARY) $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/usr/bin/
	@cp README.md $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/usr/share/doc/$(BINARY)/
	@cp LICENSE $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/usr/share/doc/$(BINARY)/copyright
	@echo "Package: $(BINARY)" > $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo "Version: $(VERSION)" >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo "Architecture: amd64" >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo "Maintainer: Andy Jeffries <andy@andyjeffries.co.uk>" >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo "Description: Fast parallel NZB downloader" >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo " A fast, parallel NZB downloader for Usenet with automatic" >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo " PAR2 verification, archive extraction, and file deobfuscation." >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo "Recommends: par2, unrar, p7zip-full, unzip" >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	@echo "Homepage: https://github.com/$(GITHUB_USER)/$(GITHUB_REPO)" >> $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	dpkg-deb --build $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64
	@mv $(DIST_DIR)/deb/$(BINARY)_$(VERSION)_amd64.deb $(DIST_DIR)/
	@rm -rf $(DIST_DIR)/deb
	@echo "Built $(DIST_DIR)/$(BINARY)_$(VERSION)_amd64.deb"
