# Third-Party Notices

Penguin Remote Client includes separately licensed components. The root
Apache-2.0 license does not replace their licenses or copyright notices.

## Runtime Components

- frp v0.71.0: Apache-2.0; see [original license](third_party/frp/LICENSE) and
  [Penguin Remote changes](third_party/frp/REMOTE_CHANGES.md). Original source
  copyright headers are retained.
- Go standard library: BSD-style Go license and patent grant, included with
  [Go dependency license texts](third_party/licenses/go.txt).
- Electron 43.7.3: MIT; see [Electron license](third_party/licenses/electron.txt).
- UI icon geometry adapted from Lucide/Feather: ISC and MIT; see
  [original icon license and attribution](third_party/licenses/lucide.txt).
- Chromium, Node.js and their bundled components: target-specific license
  notices are supplied by the Electron distribution. Desktop packages retain
  `LICENSE.electron.txt` and `LICENSES.chromium.html`; do not strip them.
- WireGuard Go/TUN components: MIT, with copyright attribution to WireGuard LLC
  retained in their upstream source headers and the Go license collection.

The runtime module inventory is the union of Windows amd64, macOS amd64/arm64
and Linux amd64/arm64 native-client dependency graphs with CGO disabled. Replaced
modules are attributed to the actual replacement version, not the nominal module.
Platform-specific modules need not be present in every executable.

| Go Component | Version | Targets |
| --- | --- | --- |
| `./third_party/frp` | `v0.71.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/armon/go-socks5` | `v0.0.0-20160902184237-e75332964ef5` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/Azure/go-ntlmssp` | `v0.1.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/coreos/go-oidc/v3` | `v3.18.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/fatedier/golib` | `v0.8.2` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/fatedier/yamux` | `v0.0.0-20250825093530-d0154be01cd6` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/go-jose/go-jose/v4` | `v4.1.4` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/golang/snappy` | `v0.0.4` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/gorilla/mux` | `v1.8.1` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/inconshreveable/mousetrap` | `v1.1.0` | windows/amd64 |
| `github.com/klauspost/cpuid/v2` | `v2.2.6` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/klauspost/reedsolomon` | `v1.12.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/pelletier/go-toml/v2` | `v2.2.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/pires/go-proxyproto` | `v0.15.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/pkg/errors` | `v0.9.1` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/quic-go/quic-go` | `v0.60.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/samber/lo` | `v1.47.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/songgao/water` | `v0.0.0-20200317203138-2b4b6d7c09d8` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/spf13/cobra` | `v1.8.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/spf13/pflag` | `v1.0.5` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/templexxx/cpu` | `v0.1.1` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/templexxx/xorsimd` | `v0.4.3` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/tjfoc/gmsm` | `v1.4.1` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `github.com/vishvananda/netlink` | `v1.3.0` | linux/amd64, linux/arm64 |
| `github.com/vishvananda/netns` | `v0.0.4` | linux/amd64, linux/arm64 |
| `github.com/xtaci/kcp-go/v5` | `v5.6.13` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.org/x/crypto` | `v0.54.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.org/x/net` | `v0.56.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.org/x/oauth2` | `v0.36.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.org/x/sync` | `v0.22.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.org/x/sys` | `v0.47.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.org/x/text` | `v0.40.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.org/x/time` | `v0.10.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `golang.zx2c4.com/wintun` | `v0.0.0-20230126152724-0fa3db229ce2` | windows/amd64 |
| `golang.zx2c4.com/wireguard` | `v0.0.0-20231211153847-12269c276173` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `gopkg.in/ini.v1` | `v1.67.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `gopkg.in/yaml.v2` | `v2.4.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `k8s.io/apimachinery` | `v0.28.8` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `k8s.io/utils` | `v0.0.0-20230406110748-d93618cff8a2` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `sigs.k8s.io/json` | `v0.0.0-20221116044647-bc3834ca7abd` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |
| `sigs.k8s.io/yaml` | `v1.3.0` | windows/amd64, darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 |

## Build And Test Tools

The following installed npm tools support development, testing or packaging.
They are not runtime npm dependencies of the desktop application. Their declared
licenses and original available license/notice files are recorded for
transparency in [the build-tool collection](third_party/licenses/build-tools.txt)
and [the manifest](third_party/licenses/manifest.json). Packages that publish no
standalone license file are explicitly identified; their published README is
retained instead. Platform-specific optional tools absent from this Windows
installation are not claimed as individually inventoried or redistributed.

| Package | Version | Declared License | Standalone License In npm Package |
| --- | --- | --- | --- |
| `@electron-internal/extract-zip` | `1.0.5` | BSD-2-Clause | No; published README included |
| `@electron/asar` | `3.4.1` | MIT | Included |
| `@electron/fuses` | `1.8.0` | MIT | Included |
| `@electron/get` | `3.1.0` | MIT | Included |
| `@electron/get` | `5.1.0` | MIT | Included |
| `@electron/notarize` | `2.5.0` | MIT | Included |
| `@electron/osx-sign` | `1.3.3` | BSD-2-Clause | Included |
| `@electron/rebuild` | `4.2.0` | MIT | Included |
| `@electron/universal` | `2.0.3` | MIT | Included |
| `@electron/windows-sign` | `1.2.2` | BSD-2-Clause | Included |
| `@esbuild/win32-x64` | `0.27.7` | MIT | No; published README included |
| `@esbuild/win32-x64` | `0.28.2` | MIT | No; published README included |
| `@isaacs/fs-minipass` | `4.0.1` | ISC | Included |
| `@jridgewell/gen-mapping` | `0.3.13` | MIT | Included |
| `@jridgewell/resolve-uri` | `3.1.2` | MIT | Included |
| `@jridgewell/sourcemap-codec` | `1.6.0` | MIT | Included |
| `@jridgewell/trace-mapping` | `0.3.31` | MIT | Included |
| `@malept/cross-spawn-promise` | `2.0.0` | Apache-2.0 | Included |
| `@malept/flatpak-bundler` | `0.4.0` | MIT | Included |
| `@noble/hashes` | `1.8.0` | MIT | Included |
| `@peculiar/asn1-schema` | `2.9.5` | MIT | Included |
| `@peculiar/json-schema` | `1.1.12` | MIT | Included |
| `@peculiar/utils` | `2.0.3` | MIT | Included |
| `@peculiar/webcrypto` | `1.7.1` | MIT | Included |
| `@rollup/rollup-win32-x64-gnu` | `4.63.4` | MIT | No; published README included |
| `@rollup/rollup-win32-x64-msvc` | `4.63.4` | MIT | No; published README included |
| `@sindresorhus/is` | `4.6.0` | MIT | Included |
| `@szmarczak/http-timer` | `4.0.6` | MIT | Included |
| `@types/cacheable-request` | `6.0.3` | MIT | Included |
| `@types/chai` | `5.2.3` | MIT | Included |
| `@types/debug` | `4.1.13` | MIT | Included |
| `@types/deep-eql` | `4.0.2` | MIT | Included |
| `@types/estree` | `1.0.9` | MIT | Included |
| `@types/fs-extra` | `9.0.13` | MIT | Included |
| `@types/http-cache-semantics` | `4.2.0` | MIT | Included |
| `@types/keyv` | `3.1.4` | MIT | Included |
| `@types/ms` | `2.1.0` | MIT | Included |
| `@types/node` | `24.13.6` | MIT | Included |
| `@types/responselike` | `1.0.3` | MIT | Included |
| `@vitest/expect` | `3.2.7` | MIT | Included |
| `@vitest/mocker` | `3.2.7` | MIT | Included |
| `@vitest/pretty-format` | `3.2.7` | MIT | Included |
| `@vitest/runner` | `3.2.7` | MIT | Included |
| `@vitest/snapshot` | `3.2.7` | MIT | Included |
| `@vitest/spy` | `3.2.7` | MIT | Included |
| `@vitest/utils` | `3.2.7` | MIT | Included |
| `@xmldom/xmldom` | `0.8.15` | MIT | Included |
| `abbrev` | `4.0.0` | ISC | Included |
| `acorn` | `8.18.0` | MIT | Included |
| `agent-base` | `7.1.4` | MIT | Included |
| `ajv` | `8.20.0` | MIT | Included |
| `ansi-regex` | `5.0.1` | MIT | Included |
| `ansi-styles` | `4.3.0` | MIT | Included |
| `any-promise` | `1.3.0` | MIT | Included |
| `app-builder-lib` | `26.16.1` | MIT | No; published README included |
| `argparse` | `2.0.1` | Python-2.0 | Included |
| `asn1js` | `3.0.10` | BSD-3-Clause | Included |
| `assertion-error` | `2.0.1` | MIT | Included |
| `async-exit-hook` | `2.0.1` | MIT | Included |
| `async` | `3.2.6` | MIT | Included |
| `asynckit` | `0.4.0` | MIT | Included |
| `at-least-node` | `1.0.0` | ISC | Included |
| `aws4` | `1.13.2` | MIT | Included |
| `balanced-match` | `1.0.2` | MIT | Included |
| `balanced-match` | `4.0.4` | MIT | Included |
| `base64-js` | `1.5.1` | MIT | Included |
| `bluebird` | `3.7.2` | MIT | Included |
| `boolean` | `3.2.0` | MIT | Included |
| `brace-expansion` | `1.1.21` | MIT | Included |
| `brace-expansion` | `2.1.7` | MIT | Included |
| `brace-expansion` | `5.0.12` | MIT | Included |
| `buffer-from` | `1.1.2` | MIT | Included |
| `builder-util-runtime` | `9.7.0` | MIT | Included |
| `builder-util` | `26.16.0` | MIT | Included |
| `bundle-require` | `5.1.0` | MIT | Included |
| `bytestreamjs` | `2.0.1` | BSD-3-Clause | Included |
| `cac` | `6.7.14` | MIT | Included |
| `cacheable-lookup` | `5.0.4` | MIT | Included |
| `cacheable-request` | `7.0.4` | MIT | Included |
| `call-bind-apply-helpers` | `1.0.2` | MIT | Included |
| `chai` | `5.3.3` | MIT | Included |
| `chalk` | `4.1.2` | MIT | Included |
| `check-error` | `2.1.3` | MIT | Included |
| `chokidar` | `4.0.3` | MIT | Included |
| `chownr` | `3.0.0` | BlueOak-1.0.0 | Included |
| `chromium-pickle-js` | `0.2.0` | MIT | No; published README included |
| `ci-info` | `4.3.1` | MIT | Included |
| `ci-info` | `4.4.0` | MIT | Included |
| `cliui` | `8.0.1` | ISC | Included |
| `clone-response` | `1.0.3` | MIT | Included |
| `color-convert` | `2.0.1` | MIT | Included |
| `color-name` | `1.1.4` | MIT | Included |
| `combined-stream` | `1.0.8` | MIT | Included |
| `commander` | `4.1.1` | MIT | Included |
| `commander` | `5.1.0` | MIT | Included |
| `commander` | `9.5.0` | MIT | Included |
| `compare-version` | `0.1.2` | MIT | No; published README included |
| `concat-map` | `0.0.1` | MIT | Included |
| `confbox` | `0.1.8` | MIT | Included |
| `consola` | `3.4.2` | MIT | Included |
| `core-util-is` | `1.0.3` | MIT | Included |
| `cross-dirname` | `0.1.0` | MIT | No; published README included |
| `cross-spawn` | `7.0.6` | MIT | Included |
| `debug` | `4.4.3` | MIT | Included |
| `decompress-response` | `6.0.0` | MIT | Included |
| `deep-eql` | `5.0.2` | MIT | Included |
| `defer-to-connect` | `2.0.1` | MIT | Included |
| `define-data-property` | `1.1.4` | MIT | Included |
| `define-properties` | `1.2.1` | MIT | Included |
| `delayed-stream` | `1.0.0` | MIT | Included |
| `detect-node` | `2.1.0` | MIT | Included |
| `dir-compare` | `4.2.0` | MIT | Included |
| `dmg-builder` | `26.16.1` | MIT | No; published README included |
| `dotenv-expand` | `11.0.7` | BSD-2-Clause | Included |
| `dotenv` | `16.6.1` | BSD-2-Clause | Included |
| `dunder-proto` | `1.0.1` | MIT | Included |
| `duplexer2` | `0.1.4` | BSD-3-Clause | Included |
| `ejs` | `3.1.10` | Apache-2.0 | Included |
| `electron-builder-squirrel-windows` | `26.16.1` | MIT | Included |
| `electron-builder` | `26.16.1` | MIT | Included |
| `electron-publish` | `26.16.0` | MIT | Included |
| `electron-winstaller` | `5.4.0` | MIT | Included |
| `electron` | `43.7.3` | MIT | Included |
| `emoji-regex` | `8.0.0` | MIT | Included |
| `end-of-stream` | `1.4.5` | MIT | Included |
| `env-paths` | `2.2.1` | MIT | Included |
| `env-paths` | `3.0.0` | MIT | Included |
| `err-code` | `2.0.3` | MIT | No; published README included |
| `es-define-property` | `1.0.1` | MIT | Included |
| `es-errors` | `1.3.0` | MIT | Included |
| `es-module-lexer` | `1.7.0` | MIT | Included |
| `es-object-atoms` | `1.1.2` | MIT | Included |
| `es-set-tostringtag` | `2.1.0` | MIT | Included |
| `es6-error` | `4.1.1` | MIT | Included |
| `esbuild` | `0.27.7` | MIT | Included |
| `esbuild` | `0.28.2` | MIT | Included |
| `escalade` | `3.2.0` | MIT | Included |
| `escape-string-regexp` | `4.0.0` | MIT | Included |
| `estree-walker` | `3.0.3` | MIT | Included |
| `expect-type` | `1.4.0` | Apache-2.0 | Included |
| `exponential-backoff` | `3.1.3` | Apache-2.0 | Included |
| `fast-deep-equal` | `3.1.3` | MIT | Included |
| `fast-uri` | `3.1.8` | BSD-3-Clause | Included |
| `fdir` | `6.5.0` | MIT | Included |
| `filelist` | `1.0.6` | Apache-2.0 | No; published README included |
| `fix-dts-default-cjs-exports` | `1.0.1` | MIT | Included |
| `form-data` | `4.0.6` | MIT | Included |
| `fs-extra` | `10.1.0` | MIT | Included |
| `fs-extra` | `11.3.1` | MIT | Included |
| `fs-extra` | `11.4.0` | MIT | Included |
| `fs-extra` | `7.0.1` | MIT | Included |
| `fs-extra` | `8.1.0` | MIT | Included |
| `fs-extra` | `9.1.0` | MIT | Included |
| `fs.realpath` | `1.0.0` | ISC | Included |
| `function-bind` | `1.1.2` | MIT | Included |
| `get-caller-file` | `2.0.5` | ISC | Included |
| `get-intrinsic` | `1.3.0` | MIT | Included |
| `get-proto` | `1.0.1` | MIT | Included |
| `get-stream` | `5.2.0` | MIT | Included |
| `glob` | `7.2.3` | ISC | Included |
| `global-agent` | `3.0.0` | BSD-3-Clause | Included |
| `globalthis` | `1.0.4` | MIT | Included |
| `gopd` | `1.2.0` | MIT | Included |
| `got` | `11.8.6` | MIT | Included |
| `graceful-fs` | `4.2.11` | ISC | Included |
| `has-flag` | `4.0.0` | MIT | Included |
| `has-property-descriptors` | `1.0.2` | MIT | Included |
| `has-symbols` | `1.1.0` | MIT | Included |
| `has-tostringtag` | `1.0.2` | MIT | Included |
| `hasown` | `2.0.4` | MIT | Included |
| `hosted-git-info` | `4.1.0` | ISC | Included |
| `http-cache-semantics` | `4.2.0` | BSD-2-Clause | Included |
| `http-proxy-agent` | `7.0.2` | MIT | Included |
| `http2-wrapper` | `1.0.3` | MIT | Included |
| `https-proxy-agent` | `7.0.6` | MIT | Included |
| `inflight` | `1.0.6` | ISC | Included |
| `inherits` | `2.0.4` | ISC | Included |
| `is-fullwidth-code-point` | `3.0.0` | MIT | Included |
| `isarray` | `1.0.0` | MIT | No; published README included |
| `isbinaryfile` | `4.0.10` | MIT | Included |
| `isbinaryfile` | `5.0.7` | MIT | Included |
| `isexe` | `2.0.0` | ISC | Included |
| `isexe` | `3.1.5` | BlueOak-1.0.0 | Included |
| `isexe` | `4.0.0` | BlueOak-1.0.0 | Included |
| `jake` | `10.9.4` | Apache-2.0 | No; published README included |
| `jiti` | `2.7.0` | MIT | Included |
| `joycon` | `3.1.1` | MIT | Included |
| `js-tokens` | `9.0.1` | MIT | Included |
| `js-yaml` | `4.3.2` | MIT | Included |
| `json-buffer` | `3.0.1` | MIT | Included |
| `json-schema-traverse` | `1.0.0` | MIT | Included |
| `json-stringify-safe` | `5.0.1` | ISC | Included |
| `json5` | `2.2.3` | MIT | Included |
| `jsonfile` | `4.0.0` | MIT | Included |
| `jsonfile` | `6.2.1` | MIT | Included |
| `keyv` | `4.5.4` | MIT | No; published README included |
| `lazy-val` | `1.0.5` | MIT | No; published README included |
| `lilconfig` | `3.1.3` | MIT | Included |
| `lines-and-columns` | `1.2.4` | MIT | Included |
| `load-tsconfig` | `0.2.5` | MIT | Included |
| `lodash` | `4.18.1` | MIT | Included |
| `loupe` | `3.2.1` | MIT | Included |
| `lowercase-keys` | `2.0.0` | MIT | Included |
| `lru-cache` | `6.0.0` | ISC | Included |
| `magic-string` | `0.30.21` | MIT | Included |
| `matcher` | `3.0.0` | MIT | Included |
| `math-intrinsics` | `1.1.0` | MIT | Included |
| `mime-db` | `1.52.0` | MIT | Included |
| `mime-types` | `2.1.35` | MIT | Included |
| `mime` | `2.6.0` | MIT | Included |
| `mimic-response` | `1.0.1` | MIT | Included |
| `mimic-response` | `3.1.0` | MIT | Included |
| `minimatch` | `10.2.6` | BlueOak-1.0.0 | Included |
| `minimatch` | `3.1.5` | ISC | Included |
| `minimatch` | `5.1.9` | ISC | Included |
| `minimatch` | `9.0.9` | ISC | Included |
| `minimist` | `1.2.8` | MIT | Included |
| `minipass` | `7.1.3` | BlueOak-1.0.0 | Included |
| `minizlib` | `3.1.0` | MIT | Included |
| `mkdirp` | `0.5.6` | MIT | Included |
| `mlly` | `1.8.2` | MIT | Included |
| `ms` | `2.1.3` | MIT | Included |
| `mz` | `2.7.0` | MIT | Included |
| `nanoid` | `3.3.19` | MIT | Included |
| `node-abi` | `4.35.0` | MIT | Included |
| `node-api-version` | `0.2.1` | MIT | No; published README included |
| `node-gyp` | `12.4.0` | MIT | Included |
| `node-int64` | `0.4.0` | MIT | Included |
| `nopt` | `9.0.0` | ISC | Included |
| `normalize-url` | `6.1.0` | MIT | Included |
| `object-assign` | `4.1.1` | MIT | Included |
| `object-keys` | `1.1.1` | MIT | Included |
| `once` | `1.4.0` | ISC | Included |
| `p-cancelable` | `2.1.1` | MIT | Included |
| `p-limit` | `3.1.0` | MIT | Included |
| `path-is-absolute` | `1.0.1` | MIT | Included |
| `path-key` | `3.1.1` | MIT | Included |
| `pathe` | `2.0.3` | MIT | Included |
| `pathval` | `2.0.1` | MIT | Included |
| `pe-library` | `0.4.1` | MIT | Included |
| `picocolors` | `1.1.1` | ISC | Included |
| `picomatch` | `4.0.7` | MIT | Included |
| `pirates` | `4.0.7` | MIT | Included |
| `pkg-types` | `1.3.1` | MIT | Included |
| `pkijs` | `3.4.1` | BSD-3-Clause | Included |
| `plist` | `3.1.0` | MIT | Included |
| `postcss-load-config` | `6.0.1` | MIT | Included |
| `postcss` | `8.5.28` | MIT | Included |
| `postject` | `1.0.0-alpha.6` | MIT | Included |
| `proc-log` | `6.1.0` | ISC | Included |
| `process-nextick-args` | `2.0.1` | MIT | Included |
| `progress` | `2.0.3` | MIT | Included |
| `promise-retry` | `2.0.1` | MIT | Included |
| `proper-lockfile` | `4.1.2` | MIT | Included |
| `pump` | `3.0.4` | MIT | Included |
| `pvtsutils` | `1.3.6` | MIT | Included |
| `pvutils` | `1.2.0` | MIT | Included |
| `quick-lru` | `5.1.1` | MIT | Included |
| `read-binary-file-arch` | `1.0.6` | MIT | No; published README included |
| `readable-stream` | `2.3.8` | MIT | Included |
| `readdirp` | `4.1.2` | MIT | Included |
| `require-directory` | `2.1.1` | MIT | Included |
| `require-from-string` | `2.0.2` | MIT | Included |
| `resedit` | `1.7.2` | MIT | Included |
| `resolve-alpn` | `1.2.1` | MIT | Included |
| `resolve-from` | `5.0.0` | MIT | Included |
| `responselike` | `2.0.1` | MIT | Included |
| `retry` | `0.12.0` | MIT | Included |
| `rimraf` | `2.6.3` | ISC | Included |
| `roarr` | `2.15.4` | BSD-3-Clause | Included |
| `rollup` | `4.63.4` | MIT | Included |
| `safe-buffer` | `5.1.2` | MIT | Included |
| `sanitize-filename` | `1.6.4` | WTFPL OR ISC | Included |
| `sax` | `1.6.1` | BlueOak-1.0.0 | Included |
| `semver-compare` | `1.0.0` | MIT | Included |
| `semver` | `5.7.2` | ISC | Included |
| `semver` | `6.3.1` | ISC | Included |
| `semver` | `7.7.4` | ISC | Included |
| `semver` | `7.8.5` | ISC | Included |
| `serialize-error` | `7.0.1` | MIT | Included |
| `shebang-command` | `2.0.0` | MIT | Included |
| `shebang-regex` | `3.0.0` | MIT | Included |
| `siginfo` | `2.0.0` | ISC | Included |
| `signal-exit` | `3.0.7` | ISC | Included |
| `simple-update-notifier` | `2.0.0` | MIT | Included |
| `source-map-js` | `1.2.1` | BSD-3-Clause | Included |
| `source-map-support` | `0.5.21` | MIT | Included |
| `source-map` | `0.6.1` | BSD-3-Clause | Included |
| `source-map` | `0.7.6` | BSD-3-Clause | Included |
| `sprintf-js` | `1.1.3` | BSD-3-Clause | Included |
| `stackback` | `0.0.2` | MIT | No; published README included |
| `stat-mode` | `1.0.0` | MIT | Included |
| `std-env` | `3.10.0` | MIT | Included |
| `string_decoder` | `1.1.1` | MIT | Included |
| `string-width` | `4.2.3` | MIT | Included |
| `strip-ansi` | `6.0.1` | MIT | Included |
| `strip-literal` | `3.1.0` | MIT | Included |
| `sucrase` | `3.35.1` | MIT | Included |
| `sumchecker` | `3.0.1` | Apache-2.0 | Included |
| `supports-color` | `7.2.0` | MIT | Included |
| `tar` | `7.5.22` | BlueOak-1.0.0 | Included |
| `temp-file` | `3.4.0` | MIT | No; published README included |
| `temp` | `0.9.4` | MIT | Included |
| `thenify-all` | `1.6.0` | MIT | Included |
| `thenify` | `3.3.1` | MIT | Included |
| `tiny-async-pool` | `1.3.0` | MIT | Included |
| `tinybench` | `2.9.0` | MIT | Included |
| `tinyexec` | `0.3.2` | MIT | Included |
| `tinyglobby` | `0.2.17` | MIT | Included |
| `tinypool` | `1.1.1` | MIT | Included |
| `tinyrainbow` | `2.0.0` | MIT | Included |
| `tinyspy` | `4.0.6` | MIT | Included |
| `tmp-promise` | `3.0.3` | MIT | No; published README included |
| `tmp` | `0.2.7` | MIT | Included |
| `tree-kill` | `1.2.2` | MIT | Included |
| `truncate-utf8-bytes` | `1.0.2` | WTFPL | No; published README included |
| `ts-interface-checker` | `0.1.13` | Apache-2.0 | Included |
| `tslib` | `2.8.1` | 0BSD | Included |
| `tsup` | `8.5.1` | MIT | Included |
| `type-fest` | `0.13.1` | (MIT OR CC0-1.0) | Included |
| `typescript` | `5.9.3` | Apache-2.0 | Included |
| `ufo` | `1.6.4` | MIT | Included |
| `undici-types` | `7.18.2` | MIT | Included |
| `undici` | `6.28.1` | MIT | Included |
| `undici` | `7.29.1` | MIT | Included |
| `universalify` | `0.1.2` | MIT | Included |
| `universalify` | `2.0.1` | MIT | Included |
| `unzipper` | `0.12.5` | MIT | Included |
| `utf8-byte-length` | `1.0.5` | (WTFPL OR MIT) | Included |
| `util-deprecate` | `1.0.2` | MIT | Included |
| `vite-node` | `3.2.4` | MIT | Included |
| `vite` | `7.3.6` | MIT | Included |
| `vitest` | `3.2.7` | MIT | Included |
| `webcrypto-core` | `1.9.2` | MIT | Included |
| `which` | `2.0.2` | ISC | Included |
| `which` | `5.0.0` | ISC | Included |
| `which` | `6.0.1` | ISC | Included |
| `why-is-node-running` | `2.3.0` | MIT | Included |
| `wrap-ansi` | `7.0.0` | MIT | Included |
| `wrappy` | `1.0.2` | ISC | Included |
| `xmlbuilder` | `15.1.1` | MIT | Included |
| `y18n` | `5.0.8` | ISC | Included |
| `yallist` | `4.0.0` | ISC | Included |
| `yallist` | `5.0.0` | BlueOak-1.0.0 | Included |
| `yargs-parser` | `21.1.1` | ISC | Included |
| `yargs` | `17.7.3` | MIT | Included |
| `yocto-queue` | `0.1.0` | MIT | Included |

## Distribution And Maintenance

Desktop builds copy the project license, NOTICE, this index, license collection
and frp license/change record into `resources/legal`. Electron runtime licenses
remain in their platform-specific standard location. Standalone native binary
distributions must include LICENSE, NOTICE, this index and the license
collection as companion files. The repository does not grant trademark rights
or access to the official subscription service.

When dependency versions change, regenerate and review the actual supported
platform inventories and upstream licenses before refreshing the manifest. The
packaging check compares normalized dependency-file SHA-256 values and Electron
versions to prevent silently shipping a stale notice collection. This inventory
is attribution, not a claim of ownership, legal advice or a vulnerability audit.
