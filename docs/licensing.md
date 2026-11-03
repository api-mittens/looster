# Licensing and third-party software

Looster's original code and documentation are available under the [MIT license](../LICENSE).
Third-party code retains its own licenses. MIT does not replace or override them.
The supplied project logo is treated as project artwork, not as a third-party dependency.

## Dependency review

The checked-in [third-party notices](../THIRD_PARTY_NOTICES.md) contain exact module
versions, upstream source locations, copyright notices, and full license texts.
The inventory covers the Go packages used by the application and its tests,
platform-specific packages for all six release targets, the Go toolchain, and the
complete bundled D3 distribution, including its transitive components. Test-only
libraries are identified separately and are not linked into production binaries.
Additional upstream notices may be retained conservatively even where the relevant
upstream examples or tests are not used.

The current review found permissive MIT, BSD, ISC, Apache-2.0, and public-domain
terms, plus **MPL-2.0 in `github.com/hashicorp/hcl`**, a transitive production
dependency of Viper. Some dependencies have multiple license texts, including
`go-toml`'s MIT and Apache-2.0 components. Preserve the complete notices, not just a
single license label. Goja's test-only numeric conversion code also includes a
Lucent permissive notice requiring retained documentation notices and
non-endorsement. Its complete text is retained rather than relabeled MIT.

### HCL source availability

HCL is used unmodified. Its covered source files remain under MPL-2.0, not MIT.
The exact source version and public Go module source archive URL are recorded in
its section of the third-party notices. Release archives additionally include
`third_party_sources/hashicorp-hcl-<version>.zip`, copied from the checksum-verified
Go module cache. That archive contains the covered source and upstream license
notices. Users installing only the executable can obtain the same source through
the URL printed by `looster licenses`.

If you modify HCL's covered files, retain their notices, mark your changes where
required, publish the modified covered source under MPL-2.0, and update the source
archive and source-location notice. Looster's separate original files can remain
MIT licensed. Do not assume a future version of a dependency has the same license.

## Distribution

Every binary embeds Looster's MIT license and the third-party notice bundle:

```sh
looster licenses
```

This command is offline and does not load configuration, connect to PostgreSQL,
or require a running worker. The checksum-verifying installer installs this same
binary, so it does not discard the notices when it extracts only the executable.
Release archives also carry `LICENSE`, `THIRD_PARTY_NOTICES.md`, the HCL source
archive, and the existing D3 license files under the embedded web assets.
When redistributing a binary, preserve its notices and source-availability notice.
When redistributing source, retain the license files, bundled vendor notices, and
upstream copyright headers. No upstream dependency source has been modified by
this licensing change.

## Updating dependencies

From the repository root, with the Go version specified by `go.mod`:

```sh
go run ./scripts/licenses
go run ./scripts/licenses -check
go test ./...
```

Review the generated diff and upstream license terms before committing updates.
CI checks that the notice bundle matches the selected dependency versions and
license texts. The generator is a retention and drift check, not a substitute for
reviewing new licenses or checking upstream provenance. It cannot establish who
owns newly contributed code or artwork. D3 is vendored as a pinned distribution:
when replacing it, review and update its complete bundled dependency inventory
and notices as described in `web/static/vendor/README.md`.

## External services and development tools

PostgreSQL, LocalStack, Chromium, Jcode, GitHub Actions, and separately configured
model providers are not relicensed by Looster. The repository references or invokes
them, but Looster release archives do not redistribute their executables or
container images. Operators must follow their respective licenses and service
terms. If you redistribute an image, browser, harness, or other external service,
audit that distribution separately, including its operating-system packages.

## Primary references

- MIT: <https://opensource.org/license/mit>
- Apache-2.0 redistribution requirements: <https://www.apache.org/licenses/LICENSE-2.0>
- MPL-2.0: <https://www.mozilla.org/MPL/2.0/>
- Mozilla's distribution FAQ: <https://www.mozilla.org/MPL/2.0/FAQ/>

This records the engineering compliance measures and the scope reviewed. It is
not a legal opinion or a guarantee covering future dependencies or contributions.
