# Changelog

## [2.0.0](https://github.com/mywio/git-ops/compare/v1.0.0...v2.0.0) (2026-08-11)


### ⚠ BREAKING CHANGES

* **core:** CapabilitySystem and CapabilityDeployer were removed. Plugins now advertise exact Execute actions such as system_info, list_deployments, stream_logs, and stack actions.

### Features

* add stack control actions to reconciler and UI ([307c43e](https://github.com/mywio/git-ops/commit/307c43eeb29efd50fb84bf144c13add836a6c0a9))
* add ui deep links and health endpoint ([c44fc54](https://github.com/mywio/git-ops/commit/c44fc540a4bbcddc568c6299a5aa781ebb2dd639))
* **core:** use action capabilities for plugin execution ([2b14fe0](https://github.com/mywio/git-ops/commit/2b14fe08669a381946f1a3992c20d3a9ec6320c7))
* **plugins:** add compose refresh action ([fccb02c](https://github.com/mywio/git-ops/commit/fccb02c336941f57f1a891daeb87917f29f9ac56))
* **reconciler:** expose deployment groups ([e7a5825](https://github.com/mywio/git-ops/commit/e7a5825fe83228533548dd835b5c9aa52a4a91f5))
* require ui auth header by default ([d48fbd9](https://github.com/mywio/git-ops/commit/d48fbd92509246ba0e5dac878c5a668e00577127))
* **ui:** group deployments by source ([2521a88](https://github.com/mywio/git-ops/commit/2521a886778d47bb203612f4c17f32a07dad2aa9))


### Bug Fixes

* **config:** parse UI auth trust environment ([a82ddc9](https://github.com/mywio/git-ops/commit/a82ddc93942eabc00af55f4b37273c7a1a7a23e8))
* **core:** bound graceful shutdown ([447a1c0](https://github.com/mywio/git-ops/commit/447a1c01de969ced5872cf10bb8afc965f658119))
* **core:** fail closed when plugins cannot load ([180bd3e](https://github.com/mywio/git-ops/commit/180bd3e66f85e5770a35308c00015d25afd97b79))
* **core:** order startup event subscriptions ([8598fcb](https://github.com/mywio/git-ops/commit/8598fcb3b74e5d4c6c1744da2f1d884bc52ec807))
* fix for file deletion ([eff4f67](https://github.com/mywio/git-ops/commit/eff4f677b538ac614928b890faa978fbacebc480))
* **reconciler:** cancel log stream processes ([702c822](https://github.com/mywio/git-ops/commit/702c822c3918907bb5c0affc28eafefd7720b3e8))
* **reconciler:** load stack env for live logs ([5465de3](https://github.com/mywio/git-ops/commit/5465de3f1e8ca09f43a067a239c6083cfebb9ce3))
* **reconciler:** recover unchanged stopped stacks ([c61e31f](https://github.com/mywio/git-ops/commit/c61e31f5e3210b3cb5b12d6af2605a8920fbc341))
* redact reconciler token in config view ([2d072d9](https://github.com/mywio/git-ops/commit/2d072d994abe5e11aea9cb166123553b0e38c936))
* replace deprecated strings title usage ([f792b37](https://github.com/mywio/git-ops/commit/f792b37856e70acc37fc85bc9054da160797133d))
* restore loader and hook compatibility ([e5215aa](https://github.com/mywio/git-ops/commit/e5215aa676651d3a6c1a5f9e7de84b7c394b131b))
* restore ui frontend build under strict types ([9326552](https://github.com/mywio/git-ops/commit/9326552f9134939d042f6dddb6e5d5e5fa0a6eb3))
* **security:** contain stack paths under target dir ([61ecf80](https://github.com/mywio/git-ops/commit/61ecf80ad4e47a5204a68b06c6ac4b8d6c16e761))
* **ui:** clarify compose adoption requirements ([41d5dea](https://github.com/mywio/git-ops/commit/41d5deaa52af5c06a56eefef52947cb7ce7e5f2f))
* **ui:** verify proxy authentication sessions ([cc363b4](https://github.com/mywio/git-ops/commit/cc363b4b08bbaa1219497097f03ed20f7203a98d))
* unnecessary variable declaration ([eb5b022](https://github.com/mywio/git-ops/commit/eb5b0225e3380dc42edb3a971ef8d4dae6eecf08))


### Documentation

* **deploy:** add compose adoption workflow ([a31cff4](https://github.com/mywio/git-ops/commit/a31cff4570fc3db476636676bf12f5597c8c2f38))
* link runtime architecture to parent docs ([ef0a142](https://github.com/mywio/git-ops/commit/ef0a142d70980d357e100d534d644d64f17f528d))
* **mcp:** sync embedded guides ([f85d794](https://github.com/mywio/git-ops/commit/f85d794ddc843a91ba666807c5ff10f16b11ef25))
* move maintenance notes out of readme ([3d179d5](https://github.com/mywio/git-ops/commit/3d179d5a1301dfe7a633b41ed1d7e3024347adfc))

## 1.0.0 (2026-04-18)


### Features

* Add file forwarder plugin to materialize host files as runtime paths for deployments and enhance env forwarder stats. ([6d939f6](https://github.com/mywio/git-ops/commit/6d939f6b7426776224ca4f02d5d11f9ccd9ecee4))
* add image refresh plugin ([f17beff](https://github.com/mywio/git-ops/commit/f17beff53d0224a63c7c10569fa42126c570992d))
* add operator execution hardening ([163ca7d](https://github.com/mywio/git-ops/commit/163ca7d4d8b6a01db9c41d299e1061d7de5f9deb))
* add release install script ([05368e6](https://github.com/mywio/git-ops/commit/05368e6c7e837ef8fc999f2a509af5f59313d33e))
* Add targeted stack reconciliation with force deploy options for specific stacks. ([73595c9](https://github.com/mywio/git-ops/commit/73595c9200c6b47d0d07669c14dfb54784e8a655))
* allow filtering loaded plugins ([2402b6a](https://github.com/mywio/git-ops/commit/2402b6a72352f02457bec5bc91255d9248386364))
* **core:** add event bus and yaml config registry ([0d15879](https://github.com/mywio/git-ops/commit/0d158796ab095b4bb75e2f2da3dea169b1218540))
* **core:** expose plugin API and config views ([a63375a](https://github.com/mywio/git-ops/commit/a63375a92d0df30e0207dc20b35ae332cc237b1b))
* **core:** order secret plugins and resolve conflicts ([bcc75e9](https://github.com/mywio/git-ops/commit/bcc75e93d593da69a9d5f9f7b3dfcf90679eb08e))
* **core:** refactor GHOps into modular plugin architecture ([e3ea98f](https://github.com/mywio/git-ops/commit/e3ea98f1fa9f486bf4acb4f4c2f39006dfd25eda))
* **env_forwarder:** forward allowlisted env vars ([90e471b](https://github.com/mywio/git-ops/commit/90e471b6f15e91f6d10ab22123a73089eb606e43))
* fix deploy ([0966824](https://github.com/mywio/git-ops/commit/09668243c37ea7b85f33ec09ba002128510627d1))
* improve build, ui, notifications, and webhook handling ([1705c67](https://github.com/mywio/git-ops/commit/1705c67f7414167b6ee253c87fb4a3e0769167e0))
* initial commit ([5659851](https://github.com/mywio/git-ops/commit/565985141231b0c092617ad57fa19d61aa5ca67e))
* integrate release-please for automated versioning and releases ([ee46fe8](https://github.com/mywio/git-ops/commit/ee46fe8a45d42e488c7f1e014050ce21ffcf1bfb))
* **mcp:** embed docs and expose deployment status ([fd79dae](https://github.com/mywio/git-ops/commit/fd79dae8262892cc3c3d56e19fe259cd6a52d2b9))
* **mcp:** implement http server with docker introspection endpoints ([0740a1a](https://github.com/mywio/git-ops/commit/0740a1a14f050244807ca209452ee0f707a5a13e))
* **plugins:** add GitOps reconciler plugin and event handling ([8775d9c](https://github.com/mywio/git-ops/commit/8775d9c0c1bfd3fa12c8cc1852da1ee01d687fe9))
* **plugins:** add time-based filtering to audit plugin's GetLastEvents function ([4b078f1](https://github.com/mywio/git-ops/commit/4b078f185cc08bd3bf52177d49a1b62261b43958))
* **plugins:** create UI plugin with API routes, frontend dashboard, and log streaming ([47a9980](https://github.com/mywio/git-ops/commit/47a998005b77e238833b58a84ed3c1897dd75975))
* **plugins:** expand reconciler capabilities with deployment listing, system info, and log streaming ([932de38](https://github.com/mywio/git-ops/commit/932de3820b623f1afae06fccbf4d439db46ed553))
* **plugins:** introduce audit plugin with memory and sqlite store support ([c68641f](https://github.com/mywio/git-ops/commit/c68641f7fa97569b454ba13a8e9588cb6c9ab58c))
* **reconciler:** emit deploy lifecycle events ([95bdf82](https://github.com/mywio/git-ops/commit/95bdf8233b2aed6d483aa36fdecf5f4058aa4481))
* secure secret injection and fail-safe pruning ([77636c5](https://github.com/mywio/git-ops/commit/77636c5b6dda21e7f9073c34b28d3ea0ba3da609))
* show unmanaged containers in ui logs ([09c0f47](https://github.com/mywio/git-ops/commit/09c0f47e793c5bba06561428deb51b64b7bf5aae))


### Bug Fixes

* add node toolchain to docker builder ([ad19d52](https://github.com/mywio/git-ops/commit/ad19d528c208af9127e63706ed3679a604b39d0f))
* build ui plugin after frontend assets in ci ([7d1af8a](https://github.com/mywio/git-ops/commit/7d1af8a0b68be4bb819bf4e6a7ae51420ff77e9d))
* copy full node toolchain into docker builder ([a1a3cd3](https://github.com/mywio/git-ops/commit/a1a3cd3ed1fe8d3a30139b7d7a6b92bde861b630))
* persist forwarded env across restarts ([f7076d6](https://github.com/mywio/git-ops/commit/f7076d6fbb0ab8bc2ac53e983fb4270949b7141a))
* satisfy ci errcheck linting ([743fe73](https://github.com/mywio/git-ops/commit/743fe73a68e0505fa7bad8e9d08aa2898d814ee9))


### Documentation

* add architecture, api, event, and plugin guides ([b5cf5c8](https://github.com/mywio/git-ops/commit/b5cf5c80e3acc09e6da23320e5cbc4d7a701a15c))
* add windows and mac quick-start guidance ([3212df1](https://github.com/mywio/git-ops/commit/3212df11390d4376837b807d47763394ac37d130))
* **deploy:** add deployment guide and update build/docs ([82c0c31](https://github.com/mywio/git-ops/commit/82c0c31d73d1bd32314291a7ace1c27b414747dd))
* **deploy:** add update instructions ([bc72b9e](https://github.com/mywio/git-ops/commit/bc72b9ee2f56a3b4ade51c0530cc94c4b3c6e2a2))
* **examples:** add env_forwarder example and docs ([28c8b41](https://github.com/mywio/git-ops/commit/28c8b419b209e7cbe7d54aaf610fe8a3ca752eff))
* **examples:** add google secret manager example ([2178e6f](https://github.com/mywio/git-ops/commit/2178e6f2ee34a71c730d085c6d850647d1c059ae))
* **examples:** add yaml config and improve hooks ([491097c](https://github.com/mywio/git-ops/commit/491097c93ffb60c349095908fa670b33d35df577))
* **mcp:** clarify embedded docs ([7b9fbde](https://github.com/mywio/git-ops/commit/7b9fbdee83dd796b6dda48f0359603fc93ac62a5))
* **plugins:** add READMEs for audit and reconciler plugins, update plugin docs ([6e3214b](https://github.com/mywio/git-ops/commit/6e3214bc0aa17483c060dc7da2185c533167337b))
* **plugins:** split plugin docs by folder ([ed33222](https://github.com/mywio/git-ops/commit/ed3322233d614d2673570d99dd62c6d1fc18cffb))
* **plugins:** update plugin configuration docs ([b40d719](https://github.com/mywio/git-ops/commit/b40d7196d14538775b9e0b8cccea9773119e98cf))
* simplify quick-start plugin set ([3d8fddd](https://github.com/mywio/git-ops/commit/3d8fdddf94eacb39ce02188dff040c9c6c575609))
* sync architecture and mcp embedded docs ([7c96446](https://github.com/mywio/git-ops/commit/7c96446b3d3edaa7d582fe08c99eedb714cff0f9))
