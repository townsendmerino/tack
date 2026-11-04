# Releasing the macOS app: signing, notarization, checksums, attestation

Written 2026-11-03, before the app exists, so the steps are ready when it does. Francis is already in the Apple
Developer Program. Nothing here is set up yet; the first release that ships the app does it.

A downloaded Mac app that is not signed with a Developer ID and notarized by Apple is blocked by Gatekeeper ("Apple
cannot check it for malicious software"). Signing and notarization remove that. Checksums and a GitHub build
attestation then let anyone confirm the file they downloaded is the one this repo's release workflow built.

## 1. One-time setup (Francis, by hand)

These keys can sign software as Francis, so they go from his Mac straight into GitHub. A session never handles them.

1. **Signing certificate.** Xcode → Settings → Accounts → the team → Manage Certificates → "+" → **Developer ID
   Application** (or developer.apple.com → Certificates). Needs the Account Holder role.
2. **Export it.** Keychain Access → "Developer ID Application: …" → Export as `.p12`, with a strong password.
3. **Notarization key.** App Store Connect → Users and Access → Integrations → **Team Keys** → a key with the Developer
   role. Download the `.p8` (downloadable once). Note its Key ID and the Issuer ID.
4. **Repo secrets**, from a terminal in this repo:

   ```bash
   base64 -i DeveloperID.p12 | gh secret set MACOS_CERT_P12
   gh secret set MACOS_CERT_PASSWORD        # the .p12 password
   base64 -i AuthKey_XXXXXX.p8 | gh secret set APPLE_API_KEY_P8
   gh secret set APPLE_API_KEY_ID
   gh secret set APPLE_API_ISSUER_ID
   gh secret set APPLE_TEAM_ID              # the 10-character Team ID
   ```

5. Keep the `.p12` and `.p8` in a password manager and delete loose copies. The Developer ID certificate lasts five
   years; the API key until revoked. If either leaks, revoke it in the developer portal and replace the secrets.

The same secrets also serve goinfer's Mac binaries (set them there too, in `~/tmcode/goinfer`).

## 2. The release workflow (a session builds this when the app is ready)

A `.github/workflows/release.yml` triggered by a version tag. The order matters, because signing changes the bytes and
everything after it must cover the signed bytes:

1. **Build** on a `macos-*` runner: `xcodebuild archive` for the app (XcodeGen first, per this directory's
   `project.yml`), and the Go binaries the app bundles, built for `darwin/arm64` and `darwin/amd64`.
2. **Import the certificate** into a temporary keychain from `MACOS_CERT_P12` / `MACOS_CERT_PASSWORD`, deleted at the
   end of the job.
3. **Sign** with the hardened runtime and a secure timestamp, innermost code first (bundled Go binaries and
   frameworks, then the app):
   `codesign --force --options runtime --timestamp --sign "Developer ID Application: …" <path>`,
   with an entitlements file holding only what the app needs. `xcodebuild -exportArchive` with
   `method = developer-id` can do the app's part.
4. **Notarize**: zip the app (`ditto -c -k --keepParent Tack.app Tack.zip`), then
   `xcrun notarytool submit Tack.zip --key <p8> --key-id $APPLE_API_KEY_ID --issuer $APPLE_API_ISSUER_ID --wait`.
   On failure, `xcrun notarytool log <id>` says why; the job fails and nothing is published.
5. **Staple** the ticket to the app so it opens offline: `xcrun stapler staple Tack.app`. Then package (a `.dmg` or the
   final `.zip`); a `.dmg` is signed, notarized and stapled the same way. Bare command-line binaries can't be stapled;
   Gatekeeper checks them online.
6. **Checksums**: `shasum -a 256` over every asset into `checksums.txt`.
7. **Attest**: `actions/attest-build-provenance` (pinned by SHA) with `subject-checksums: checksums.txt`, job
   permissions `id-token: write`, `attestations: write`, `contents: write`. goinfer's
   `.github/workflows/release-assets.yml` does this since 2026-11-03 and is the pattern to copy.
8. **Publish** to the GitHub Release. Creating or pushing the tag stays Francis's call.

## 3. Checking a release

On a clean Mac (or after `xattr -w com.apple.quarantine` on the download):

```bash
spctl --assess --type execute -vv Tack.app          # "accepted", source=Notarized Developer ID
codesign --verify --deep --strict -vv Tack.app
xcrun stapler validate Tack.app
gh attestation verify Tack.zip --repo townsendmerino/tack
shasum -a 256 -c checksums.txt
```

Then open the app by double-click from Downloads: it should open with no Gatekeeper warning.
