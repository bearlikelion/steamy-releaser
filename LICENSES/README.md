# Third-party source and asset notices

The project includes shadcn/ui-derived components under
`cmd/steam-releaser-desktop/frontend/src/components/ui`. Their upstream MIT
license is in [shadcn-ui-MIT.txt](shadcn-ui-MIT.txt).

The bundled Geist font is distributed under the SIL Open Font License in
[Geist-OFL.txt](Geist-OFL.txt).

The macOS app property-list templates are derived from Wails and are covered by
[Wails-MIT.txt](Wails-MIT.txt).

The npm tarball for `react-remove-scroll-bar` 2.3.8 declares the MIT license
but does not include its license text. [react-remove-scroll-bar-MIT.txt](react-remove-scroll-bar-MIT.txt)
is the [upstream project's MIT notice](https://github.com/theKashey/react-remove-scroll-bar/blob/master/LICENSE),
retained for release packaging.

Release archives also contain `THIRD_PARTY_NOTICES.txt`, generated from the
installed dependencies for each target platform, including notices in compiled
Go package and embedded-asset directories and from frontend build tools whose
code can appear in the bundle. Dependencies retain their own copyright and
license terms.
