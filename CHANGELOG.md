# Changelog

## [0.17.0](https://github.com/mholtzscher/hearth/compare/v0.16.0...v0.17.0) (2026-10-30)


### ⚠ BREAKING CHANGES

* **automations:** unify durable Step execution history ([#225](https://github.com/mholtzscher/hearth/issues/225))

### Features

* **adapters:** add stable function contracts and reserved invocations ([eda67e3](https://github.com/mholtzscher/hearth/commit/eda67e3c38c592253005350871fed7fcf6cf080e))
* **adapters:** separate protocol contracts and compact Function receipts ([10560fa](https://github.com/mholtzscher/hearth/commit/10560fa965f8f567da3f808154100ab1390c9bfc))
* **agent:** use environment-only model credentials ([a1016a6](https://github.com/mholtzscher/hearth/commit/a1016a64c196542a98f398dae4c6341f15dc1079))
* **automations:** add expression value flow to unified executions ([#226](https://github.com/mholtzscher/hearth/issues/226)) ([623d6b7](https://github.com/mholtzscher/hearth/commit/623d6b7d41bde5362e6b50284d2a351a40d7f1b9))
* **automations:** execute Function Steps with retained evidence ([a9a4dd1](https://github.com/mholtzscher/hearth/commit/a9a4dd19dfa57f8f6274cd257b9fcda889a7f397))
* **automations:** own protocol projections and canonical MCP evidence ([2d09bc8](https://github.com/mholtzscher/hearth/commit/2d09bc82e4166c35e51da9590b92389d0606e02d))
* **automations:** persist reserved Function execution evidence ([a3b37f4](https://github.com/mholtzscher/hearth/commit/a3b37f470e444e6e366e2e81e4da381d6b9dd5f2))
* **automations:** unify durable Step execution history ([#225](https://github.com/mholtzscher/hearth/issues/225)) ([01af195](https://github.com/mholtzscher/hearth/commit/01af195230087bd752ca27fef6228d438c6a2b3c))
* **devices:** separate protocol projections and exact Command receipts ([7df8e9c](https://github.com/mholtzscher/hearth/commit/7df8e9c14802e1cc41b14d8eead04aa7c4d84b11))
* **http:** diagnose unexpected failures at their response owners ([#255](https://github.com/mholtzscher/hearth/issues/255)) ([3166523](https://github.com/mholtzscher/hearth/commit/3166523c25c17f6c3cf0d113f2098b7c3dfd62c3))
* **mcp:** remove resource catalogs and shared resource machinery ([6ada2ce](https://github.com/mholtzscher/hearth/commit/6ada2ceac372e2a208f750071885379b2bd5a3a9))
* **notifications:** separate HTTP and MCP contracts and receipts ([c5773d3](https://github.com/mholtzscher/hearth/commit/c5773d36561826825c5537a373c4df9b30821ba5))
* **web:** display Automation Function definitions and execution evidence ([527e04e](https://github.com/mholtzscher/hearth/commit/527e04e6c38b8c94bd3adf14dadd646ec6bbd221))


### Bug Fixes

* **adapters:** bound reserved Function contract lookup ([99c4f2e](https://github.com/mholtzscher/hearth/commit/99c4f2ed73e35b316c00ad5e998f7f1b0d2f8c9c))
* **agent:** preserve turn failure origins ([#253](https://github.com/mholtzscher/hearth/issues/253)) ([be8afde](https://github.com/mholtzscher/hearth/commit/be8afde9079552bb6348654751907f2a9c92cd18))
* **automations:** apply error classification standards ([#228](https://github.com/mholtzscher/hearth/issues/228)) ([893cb25](https://github.com/mholtzscher/hearth/commit/893cb254a90ce9191a49dd533012b5f8001dc21b))
* **automations:** classify stored corruption as internal failure ([#247](https://github.com/mholtzscher/hearth/issues/247)) ([e13d3d9](https://github.com/mholtzscher/hearth/commit/e13d3d9422d828e4f3d24001aedc08f12385874c))
* **automations:** enforce canonical evidence bounds and localize operations ([f357f25](https://github.com/mholtzscher/hearth/commit/f357f252a8afc35e4b6d9f0f891e6b18352aa160))
* **automations:** enforce shared Run evidence lifecycle invariants ([931accb](https://github.com/mholtzscher/hearth/commit/931accba7a27e0dea64925516c9242254cfa703a))
* **automations:** order Function reservations with shutdown ([874edaa](https://github.com/mholtzscher/hearth/commit/874edaa53b3f1e84b0edae5027cc4e2c4c4325fd))
* **automations:** preserve Function reference details in MCP errors ([1a5e80e](https://github.com/mholtzscher/hearth/commit/1a5e80e4c0c42ecd13c0fc5d822306551b2a2824))
* **automations:** preserve maximum-size definitions during upgrade ([50780c5](https://github.com/mholtzscher/hearth/commit/50780c5bbecbcc7c812016cc178e60bdb5ac67d8))
* **automations:** retain primary transaction failure after rollback faults ([#262](https://github.com/mholtzscher/hearth/issues/262)) ([c603e0f](https://github.com/mholtzscher/hearth/commit/c603e0f2c0bc6bdb03f4a4e8b5a7f53de368e89b))
* **automations:** retain safe definition diagnostic context ([#260](https://github.com/mholtzscher/hearth/issues/260)) ([0d5b6a7](https://github.com/mholtzscher/hearth/commit/0d5b6a7837a90801983c2413eed649b97740f70c))
* **automations:** separate Function save failures from runtime admission ([#263](https://github.com/mholtzscher/hearth/issues/263)) ([e568845](https://github.com/mholtzscher/hearth/commit/e568845fffe92dcad1ef9a0cf3a6c26f25b5b859))
* **ci:** support golangci-lint 2.15.0 ([#193](https://github.com/mholtzscher/hearth/issues/193)) ([4cf63e7](https://github.com/mholtzscher/hearth/commit/4cf63e7e2e1f301acbfa0161b2193a980dcd77d2))
* **discord:** constrain send_message destination to configured keys ([#191](https://github.com/mholtzscher/hearth/issues/191)) ([31a6a82](https://github.com/mholtzscher/hearth/commit/31a6a82e4e6316df16543ef2bd6f661822b780a4))
* **errors:** distinguish owned cancellation from faults ([#254](https://github.com/mholtzscher/hearth/issues/254)) ([57dc16a](https://github.com/mholtzscher/hearth/commit/57dc16a3d7720e2ae99a22546deca3ac95463bd7))
* **errors:** separate server preconditions from invalid input ([#249](https://github.com/mholtzscher/hearth/issues/249)) ([3bccfc2](https://github.com/mholtzscher/hearth/commit/3bccfc2b14c2732b943cc75253fb9e7294999ecb))
* **functions:** allow regressed diagnostic timestamps ([58d8aca](https://github.com/mholtzscher/hearth/commit/58d8aca3970f2f32d94399eb3435d0c49b600eed))
* **http:** align error publication with reachable responses ([#256](https://github.com/mholtzscher/hearth/issues/256)) ([4eb6dfe](https://github.com/mholtzscher/hearth/commit/4eb6dfec17a1e94608fefcad3feaf0777352f96d))
* **mcp:** preserve field codes on null filter rejection ([#258](https://github.com/mholtzscher/hearth/issues/258)) ([d691f8d](https://github.com/mholtzscher/hearth/commit/d691f8d65b97800a5ef91939818ddeb01b751384))
* **nats:** retain safe failure event and stage metadata ([#257](https://github.com/mholtzscher/hearth/issues/257)) ([04f57c6](https://github.com/mholtzscher/hearth/commit/04f57c6474a3a666f2446a024823cdfaf9d8d6a9))
* **notifications:** reject conflicting terminal completions ([#264](https://github.com/mholtzscher/hearth/issues/264)) ([4fa7223](https://github.com/mholtzscher/hearth/commit/4fa72234cfd0353357a66b6aa4e2402b8d7770e4))
* **release:** keep breaking changes on pre-major versions ([5ff5ca6](https://github.com/mholtzscher/hearth/commit/5ff5ca6cc790e332a3c080f423a7eddf8ccaeef9))
* **sqlite:** align mappings and reject corrupt retained evidence ([#252](https://github.com/mholtzscher/hearth/issues/252)) ([cf93586](https://github.com/mholtzscher/hearth/commit/cf9358627a97d9f0d50917e28968153b09ef6e50))
* **sqlite:** canonicalize timestamp writes for development cutover ([#251](https://github.com/mholtzscher/hearth/issues/251)) ([3270ca8](https://github.com/mholtzscher/hearth/commit/3270ca88caa312bf57808bf68e9ee426211042b0))
* **web:** preserve exact Automation Function argument JSON ([9d9b552](https://github.com/mholtzscher/hearth/commit/9d9b552a9fbe579220e6c18a48ee3864ddaa65f0))


### Performance Improvements

* **automations:** index pruning and seek history cursors ([#221](https://github.com/mholtzscher/hearth/issues/221)) ([10f85e4](https://github.com/mholtzscher/hearth/commit/10f85e41abb5b994f3743ef8355ce4fcf77b4c35))
* **automations:** project history summary columns ([#223](https://github.com/mholtzscher/hearth/issues/223)) ([ba47f29](https://github.com/mholtzscher/hearth/commit/ba47f29971ec75279f67055788f204dbd5392b97))
* **automations:** share immutable definition revisions ([#224](https://github.com/mholtzscher/hearth/issues/224)) ([a6fa9af](https://github.com/mholtzscher/hearth/commit/a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0))

## [0.16.0](https://github.com/mholtzscher/hearth/compare/v0.15.1...v0.16.0) (2026-10-20)


### Features

* **adapters:** add bounded Adapter Functions and Zigbee2MQTT refresh ([#186](https://github.com/mholtzscher/hearth/issues/186)) ([94fea5f](https://github.com/mholtzscher/hearth/commit/94fea5f38d0032eb106c71db665746888521346b))
* **adapters:** project adapter health durably into Devices ([1fa4685](https://github.com/mholtzscher/hearth/commit/1fa46855d487f3c6983464904a599c5723485669))
* **discord:** add send_message adapter function ([#187](https://github.com/mholtzscher/hearth/issues/187)) ([256fabb](https://github.com/mholtzscher/hearth/commit/256fabbefd00ba0a285c5ab52e751f2a39c0da39))
* **outbox:** consolidate durable relay delivery ([#185](https://github.com/mholtzscher/hearth/issues/185)) ([dc72642](https://github.com/mholtzscher/hearth/commit/dc72642eb3d3b1bb87cc506faecab81789745df1))
* **simulator:** add scripted adapter functions ([#190](https://github.com/mholtzscher/hearth/issues/190)) ([c6182d3](https://github.com/mholtzscher/hearth/commit/c6182d3d3cfb3100ebd236c2f0a9725de2868c8b))


### Bug Fixes

* **agent:** recover from rejected tool calls ([#178](https://github.com/mholtzscher/hearth/issues/178)) ([a6335a3](https://github.com/mholtzscher/hearth/commit/a6335a3c279a09fc1c23d1e800d3ee361d337b41))

## [0.15.1](https://github.com/mholtzscher/hearth/compare/v0.15.0...v0.15.1) (2026-10-13)


### Bug Fixes

* **automations:** migrate legacy command step kinds ([#176](https://github.com/mholtzscher/hearth/issues/176)) ([7651262](https://github.com/mholtzscher/hearth/commit/7651262c42142425115f7bfc7b21d203370e4c96))

## [0.15.0](https://github.com/mholtzscher/hearth/compare/v0.14.0...v0.15.0) (2026-10-13)


### Features

* add Discord notification companion ([#172](https://github.com/mholtzscher/hearth/issues/172)) ([85d0743](https://github.com/mholtzscher/hearth/commit/85d07438b2a3b17850d7a04d22f715053657389a))
* add durable core notifications and adapter destinations ([#171](https://github.com/mholtzscher/hearth/issues/171)) ([5f48947](https://github.com/mholtzscher/hearth/commit/5f489477298936d4a27943c985d7a304b0b58d03))
* Add household cron automation triggers without replay ([#158](https://github.com/mholtzscher/hearth/issues/158)) ([32bec11](https://github.com/mholtzscher/hearth/commit/32bec1108546f3954386d08a776e096c4dee8aee))
* **automations:** add interruptible delay steps with durable history ([#164](https://github.com/mholtzscher/hearth/issues/164)) ([725e469](https://github.com/mholtzscher/hearth/commit/725e46967bf2171e2f1c9bd63689a13c87fb62e4))
* **automations:** add nested branching and durable decision history ([#162](https://github.com/mholtzscher/hearth/issues/162)) ([3af0bb4](https://github.com/mholtzscher/hearth/commit/3af0bb48bd18b2ceb1de1d97f7470b80133b7961))
* **devices:** support name overrides with json merge patch ([#153](https://github.com/mholtzscher/hearth/issues/153)) ([d40eeae](https://github.com/mholtzscher/hearth/commit/d40eeae1f865e1b7b46b6cec203bf0b5c9a3ce2b))

## [0.14.0](https://github.com/mholtzscher/hearth/compare/v0.13.0...v0.14.0) (2026-09-29)


### Features

* **skills:** add show-complexity skill ([0a9ebac](https://github.com/mholtzscher/hearth/commit/0a9ebac3f257f8e7f1cea216e862a77bc29470a5))
* **skills:** add test-audit skill with campaign workflow ([c57c357](https://github.com/mholtzscher/hearth/commit/c57c357b1b2e8988b6d06dbff458558cf215dd48))
* **zwave:** add Z-Wave JS adapter ([#113](https://github.com/mholtzscher/hearth/issues/113)) ([d07b9c8](https://github.com/mholtzscher/hearth/commit/d07b9c851798929689b9b553b884dc1f34ace2f9))

## [0.13.0](https://github.com/mholtzscher/hearth/compare/v0.12.0...v0.13.0) (2026-09-28)


### Features

* **automations:** implement held-state triggers ([#136](https://github.com/mholtzscher/hearth/issues/136)) ([5bc7f2b](https://github.com/mholtzscher/hearth/commit/5bc7f2b7b7f849af322e534bc9eb0eb417507f01))
* configure hearthd through CLI, environment, and YAML ([#140](https://github.com/mholtzscher/hearth/issues/140)) ([7fa4395](https://github.com/mholtzscher/hearth/commit/7fa4395123bed86f4480332c9e47938050f315a3))
* **simulator:** support multiple adapters per config ([#137](https://github.com/mholtzscher/hearth/issues/137)) ([1c9505d](https://github.com/mholtzscher/hearth/commit/1c9505d4a0f62eed2d1aa0d4800a421399a135d7))


### Bug Fixes

* **automations:** reject malformed observation fact values ([#150](https://github.com/mholtzscher/hearth/issues/150)) ([d571233](https://github.com/mholtzscher/hearth/commit/d57123305e1de0a6a70c440ff7d383e7ccc9c684))
* **automations:** validate observation pointers ([#131](https://github.com/mholtzscher/hearth/issues/131)) ([1a2a6b0](https://github.com/mholtzscher/hearth/commit/1a2a6b0aa7c4d35fd6c7c40458ac87d904870b72))


### Performance Improvements

* **format:** replace goimports with gci ([#149](https://github.com/mholtzscher/hearth/issues/149)) ([73d4015](https://github.com/mholtzscher/hearth/commit/73d401549a759c808afe0ebcf7d9ebc6859a59be))
* **test:** reduce suite overhead and cache embedded schemas ([#148](https://github.com/mholtzscher/hearth/issues/148)) ([2dbe0e5](https://github.com/mholtzscher/hearth/commit/2dbe0e595c724e9d22ed399c7b9dad7d16cf1546))

## [0.12.0](https://github.com/mholtzscher/hearth/compare/v0.11.0...v0.12.0) (2026-09-21)


### Features

* **agent:** promote household agent to core module ([#128](https://github.com/mholtzscher/hearth/issues/128)) ([e4405bd](https://github.com/mholtzscher/hearth/commit/e4405bd1b7fa3bd21e6b7e4d7d6b93eb5085546a))

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
