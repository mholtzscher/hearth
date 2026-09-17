# Changelog

## [0.11.0](https://github.com/mholtzscher/hearth/compare/v0.10.0...v0.11.0) (2026-09-17)


### Features

* **commands:** add household history and rework commands tab ([#122](https://github.com/mholtzscher/hearth/issues/122)) ([1598cdd](https://github.com/mholtzscher/hearth/commit/1598cddda86645dbd17e4692bd5cd64adf06e530))
* **web:** make devices the default landing tab ([#125](https://github.com/mholtzscher/hearth/issues/125)) ([dc4d0d0](https://github.com/mholtzscher/hearth/commit/dc4d0d0bef9196498a77c1253dc56b18f9aaa704))


### Bug Fixes

* **zigbee2mqtt:** accept any single-level MQTT friendly name ([#123](https://github.com/mholtzscher/hearth/issues/123)) ([4556f56](https://github.com/mholtzscher/hearth/commit/4556f56a23646aa22d13c7d646cc061cc06db2be))

## [0.10.0](https://github.com/mholtzscher/hearth/compare/v0.9.0...v0.10.0) (2026-09-17)


### Features

* **automations:** evaluate conditions from state snapshots ([#119](https://github.com/mholtzscher/hearth/issues/119)) ([d6933b2](https://github.com/mholtzscher/hearth/commit/d6933b210b40880314dbb840ea7f813a650f22d1))
* **simulator:** add scripted validation harness and publication ids ([#120](https://github.com/mholtzscher/hearth/issues/120)) ([7928a4e](https://github.com/mholtzscher/hearth/commit/7928a4ed82a0c72cf1d40b9f812f82b31ce8b4dd))
* **web:** add automations dashboard ([#117](https://github.com/mholtzscher/hearth/issues/117)) ([7e30017](https://github.com/mholtzscher/hearth/commit/7e300179f3555c3bd172b37b5c5c2b8ee8039b18))


### Bug Fixes

* **config:** report validation reasons and pre-validate devices ([#121](https://github.com/mholtzscher/hearth/issues/121)) ([ec19ed1](https://github.com/mholtzscher/hearth/commit/ec19ed154bc1728959024366b3192e4f77677d15))
* **devices:** extend stale-runtime fencing test timeout to 30s ([18aac9a](https://github.com/mholtzscher/hearth/commit/18aac9a75dd0a1d81e55e3639e0fb03ea18e3ad5))

## [0.9.0](https://github.com/mholtzscher/hearth/compare/v0.8.1...v0.9.0) (2026-09-15)


### Features

* **automations:** drive commands from device facts ([#106](https://github.com/mholtzscher/hearth/issues/106)) ([4205275](https://github.com/mholtzscher/hearth/commit/42052750fab10c3e7a5d6d7bf5cf968a82246b2f))


### Bug Fixes

* **web:** paginate device facts enrichment ([#104](https://github.com/mholtzscher/hearth/issues/104)) ([5b47096](https://github.com/mholtzscher/hearth/commit/5b4709659b8d325e1cc82c94dd89d97d4bc733f0))

## [0.8.1](https://github.com/mholtzscher/hearth/compare/v0.8.0...v0.8.1) (2026-09-13)


### Bug Fixes

* **release:** publish ecowitt adapter image ([#101](https://github.com/mholtzscher/hearth/issues/101)) ([3e7ba43](https://github.com/mholtzscher/hearth/commit/3e7ba4354fd3a560ab8fd3081ca0d1f79944463b))

## [0.8.0](https://github.com/mholtzscher/hearth/compare/v0.7.0...v0.8.0) (2026-09-13)


### Features

* **devices:** add durable entity event ingestion and history ([#89](https://github.com/mholtzscher/hearth/issues/89)) ([8aaba1d](https://github.com/mholtzscher/hearth/commit/8aaba1df808961473a19c01dc9c4e52bfefdab48))
* **ecowitt:** add MQTT weather adapter ([#100](https://github.com/mholtzscher/hearth/issues/100)) ([70f89fa](https://github.com/mholtzscher/hearth/commit/70f89faa7676ba726d72d34d05b17bce6abf19f8))
* **web:** show entity event history ([#92](https://github.com/mholtzscher/hearth/issues/92)) ([e4e5478](https://github.com/mholtzscher/hearth/commit/e4e547810c3100414dc8a79f13ceeca697080c80))
* **web:** support Tailscale remote access in dev server ([381cc53](https://github.com/mholtzscher/hearth/commit/381cc537553ffd9acc92cf85352f76c7f408a649))
* **zigbee2mqtt:** support occupancy and illuminance ([#93](https://github.com/mholtzscher/hearth/issues/93)) ([9bfee97](https://github.com/mholtzscher/hearth/commit/9bfee9770939174dcea35d822a88344b53add7bb))
* **zigbee2mqtt:** use Mosquitto broker ([#96](https://github.com/mholtzscher/hearth/issues/96)) ([b4d9342](https://github.com/mholtzscher/hearth/commit/b4d9342fbeedb152a1baa05d38f968f1340abe3c))


### Bug Fixes

* **devices:** resolve durable fact review findings ([37f381b](https://github.com/mholtzscher/hearth/commit/37f381bfb6632c7909d80c72fbaf2a695347e214))

## [0.7.0](https://github.com/mholtzscher/hearth/compare/v0.6.0...v0.7.0) (2026-09-10)


### Features

* **automations:** implement durable manual execution and management ([#70](https://github.com/mholtzscher/hearth/issues/70)) ([02c2f0e](https://github.com/mholtzscher/hearth/commit/02c2f0e3aa03f0258a73a2ae30b4c8705126294d))
* **automations:** match household cron minutes with first-fold semantics ([#72](https://github.com/mholtzscher/hearth/issues/72)) ([5803374](https://github.com/mholtzscher/hearth/commit/5803374daaeba26864a5374dc80bd1c4e1d5af69))
* **zigbee2mqtt:** add humidity and battery sensors ([#67](https://github.com/mholtzscher/hearth/issues/67)) ([f94ac04](https://github.com/mholtzscher/hearth/commit/f94ac043e7d50bd5cd69aa9deddb099a69550a3d))
* **zigbee2mqtt:** support smart plug entities ([#75](https://github.com/mholtzscher/hearth/issues/75)) ([9c76b7c](https://github.com/mholtzscher/hearth/commit/9c76b7cf15c1536e2ac3ea22c7cfd70143c20729))

## [0.6.0](https://github.com/mholtzscher/hearth/compare/v0.5.0...v0.6.0) (2026-09-08)


### Features

* **logging:** add startup and failure diagnostics ([#59](https://github.com/mholtzscher/hearth/issues/59)) ([01cd29c](https://github.com/mholtzscher/hearth/commit/01cd29c439f6575787c566fac23c29fc0b3a7acc))
* **zigbee2mqtt:** add bulb settings and actions ([#66](https://github.com/mholtzscher/hearth/issues/66)) ([e41d70f](https://github.com/mholtzscher/hearth/commit/e41d70f0e0ac11d6ad53d8c8b037fca91f6f3d78))
* **zigbee2mqtt:** add native bulb color support ([#60](https://github.com/mholtzscher/hearth/issues/60)) ([2fb6d12](https://github.com/mholtzscher/hearth/commit/2fb6d12aabd24c2acced37d3bddeddd883b8d45e))

## [0.5.0](https://github.com/mholtzscher/hearth/compare/v0.4.0...v0.5.0) (2026-09-05)


### Features

* **devices:** implement entity state history end-to-end ([#53](https://github.com/mholtzscher/hearth/issues/53)) ([7c0e74b](https://github.com/mholtzscher/hearth/commit/7c0e74b7e1cd02e9ef4d8667319fb5e658bb1954))

## [0.4.0](https://github.com/mholtzscher/hearth/compare/v0.3.0...v0.4.0) (2026-09-04)


### Features

* **web:** publish dashboard container image on release ([#54](https://github.com/mholtzscher/hearth/issues/54)) ([a92508c](https://github.com/mholtzscher/hearth/commit/a92508c9acfca05cb431f80cb692399dc737123d))

## [0.3.0](https://github.com/mholtzscher/hearth/compare/v0.2.0...v0.3.0) (2026-09-04)


### Features

* **adapter:** add owned mapping inventory ([#44](https://github.com/mholtzscher/hearth/issues/44)) ([ffd27d7](https://github.com/mholtzscher/hearth/commit/ffd27d713b7274deb7d5656682c1c2a5486838e6))
* **adapter:** add Zigbee2MQTT adapter ([#47](https://github.com/mholtzscher/hearth/issues/47)) ([6bc6371](https://github.com/mholtzscher/hearth/commit/6bc63719e19aed38a6b8b2afaff875d8b48fbc3c))
* **adapter:** plan Zigbee2MQTT entities ([#50](https://github.com/mholtzscher/hearth/issues/50)) ([b02d431](https://github.com/mholtzscher/hearth/commit/b02d431ce0e3b8ed1ede92ed3e0edc3070c72929))
* add adapter health and entity availability ([#30](https://github.com/mholtzscher/hearth/issues/30)) ([fd9d55b](https://github.com/mholtzscher/hearth/commit/fd9d55bf7d310d7c377346dc5cd0de5d256aeb9a))
* **devices:** add entity enablement ([#24](https://github.com/mholtzscher/hearth/issues/24)) ([f131515](https://github.com/mholtzscher/hearth/commit/f131515a7ef42d7bffffc7417a2cff7d408bc168))
* **web:** add hearth debug dashboard ([#49](https://github.com/mholtzscher/hearth/issues/49)) ([36b297b](https://github.com/mholtzscher/hearth/commit/36b297ba7aadc9df29ce8045eb4e045af6cfd0ad))

## [0.2.0](https://github.com/mholtzscher/hearth/compare/v0.1.0...v0.2.0) (2026-08-26)


### Features

* **hearthd:** allow non-loopback http binding ([#22](https://github.com/mholtzscher/hearth/issues/22)) ([4ebcec0](https://github.com/mholtzscher/hearth/commit/4ebcec05b1f199dbbc111ee76f2c55807ac90277))

## 0.1.0 (2026-08-26)


### Features

* add first-light foundation ([#2](https://github.com/mholtzscher/hearth/issues/2)) ([ca34522](https://github.com/mholtzscher/hearth/commit/ca345220e489a9c7d7e9ab062c40282b23861a97))
* **devices:** add audited command orchestration ([#8](https://github.com/mholtzscher/hearth/issues/8)) ([3ea6b46](https://github.com/mholtzscher/hearth/commit/3ea6b4642980af8138edf4b516e3fbc4f5fc3a89))
* **devices:** add resource discovery and command history ([#17](https://github.com/mholtzscher/hearth/issues/17)) ([cc26d09](https://github.com/mholtzscher/hearth/commit/cc26d09749fd7b4e00ba28093a41fbc06c76816d))
* **devices:** add SQLite registration and command ledger ([#5](https://github.com/mholtzscher/hearth/issues/5)) ([5b37414](https://github.com/mholtzscher/hearth/commit/5b374141061dcb334d19b659b6c6d6904d380f15))
* **devices:** support multi-entity registration ([#16](https://github.com/mholtzscher/hearth/issues/16)) ([4af8ede](https://github.com/mholtzscher/hearth/commit/4af8ede2b10af8865391fd73b9c2b4f81e0520ff))
* **hearthd:** add durable observation projection ([#7](https://github.com/mholtzscher/hearth/issues/7)) ([beb445c](https://github.com/mholtzscher/hearth/commit/beb445c3a7779636886e1d31816b79a656c45a41))
* **homeassistant:** add disposable migration adapter ([#9](https://github.com/mholtzscher/hearth/issues/9)) ([7cff2f0](https://github.com/mholtzscher/hearth/commit/7cff2f0700308e2bbb2b0dd50b26eca192b9e66f))
* **sdk:** add adapter nats contracts ([#3](https://github.com/mholtzscher/hearth/issues/3)) ([4d4d44b](https://github.com/mholtzscher/hearth/commit/4d4d44b5f56b8864873ac60bf00cdc8b3b853b2b))
