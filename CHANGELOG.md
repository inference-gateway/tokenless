# Changelog

All notable changes to this project will be documented in this file.

## [0.6.0](https://github.com/inference-gateway/tokenless/compare/v0.5.0...v0.6.0) (2026-08-05)

### ✨ Features

* **tool_loop:** add tool call approval callback ([#29](https://github.com/inference-gateway/tokenless/issues/29)) ([1454928](https://github.com/inference-gateway/tokenless/commit/145492871a2220b06526e0ce86161055b5f1527d))

### 📚 Documentation

* update tokenless skill for codebase drift ([#32](https://github.com/inference-gateway/tokenless/issues/32)) ([f6f20f8](https://github.com/inference-gateway/tokenless/commit/f6f20f8118a7a479d731bcb26e165dcc52c0a11f))

### 🔧 Miscellaneous

* change model version to 'preview' in tasks.yml ([b6d677a](https://github.com/inference-gateway/tokenless/commit/b6d677a30c5d52f2f845b65630cf0cd4adde3ca5))
* update default model version in tasks.yml ([75aef86](https://github.com/inference-gateway/tokenless/commit/75aef86594140fd73100e9e1fd1baaca8b6e5bda))

## [0.5.0](https://github.com/inference-gateway/tokenless/compare/v0.4.0...v0.5.0) (2026-08-05)

### ✨ Features

* restructure model modalities into nested input/output ([#31](https://github.com/inference-gateway/tokenless/issues/31)) ([7da93d2](https://github.com/inference-gateway/tokenless/commit/7da93d212aeb137e055c440afad92abe567d98ae))

## [0.4.0](https://github.com/inference-gateway/tokenless/compare/v0.3.0...v0.4.0) (2026-08-05)

### ✨ Features

* add ToolLoop for real tool invocation in tests and examples ([#24](https://github.com/inference-gateway/tokenless/issues/24)) ([bd99421](https://github.com/inference-gateway/tokenless/commit/bd99421d0a706f7952ac00a1b37ac21f5bd0a6d8))

### 👷 CI

* sync OpenTask Agent workflow ([#25](https://github.com/inference-gateway/tokenless/issues/25)) ([697a3a4](https://github.com/inference-gateway/tokenless/commit/697a3a4d3dff9fb8fa5f75673193c4fa0e89b273))
* sync OpenTask Agent workflow ([#26](https://github.com/inference-gateway/tokenless/issues/26)) ([ed99179](https://github.com/inference-gateway/tokenless/commit/ed99179fbde868cb6a62e927f4e7d6dcfca429a6))
* sync OpenTask Agent workflow ([#28](https://github.com/inference-gateway/tokenless/issues/28)) ([04592cf](https://github.com/inference-gateway/tokenless/commit/04592cf5cd26067dd5be7e427a07a50c33256c25))

### 🔧 Miscellaneous

* add Taskfile with build, lint, vet, fmt, test tasks ([#27](https://github.com/inference-gateway/tokenless/issues/27)) ([13489b2](https://github.com/inference-gateway/tokenless/commit/13489b2c12f03b58e50b2dbe93f50605e536ad82))

## [0.3.0](https://github.com/inference-gateway/tokenless/compare/v0.2.0...v0.3.0) (2026-08-05)

### ✨ Features

* add expect blocks and rename harness to root tokenless package ([#17](https://github.com/inference-gateway/tokenless/issues/17)) ([eedc693](https://github.com/inference-gateway/tokenless/commit/eedc6939a8b4c4fba354a7f688314f62936c983d))
* **examples:** restructure and add failure injection, multi-turn, binary, image, and custom model examples ([#18](https://github.com/inference-gateway/tokenless/issues/18)) ([2559ab2](https://github.com/inference-gateway/tokenless/commit/2559ab22384187baae57640a926f1d93f4c23f8a))
* **gateway:** add optional model filter to Scenario spec ([#12](https://github.com/inference-gateway/tokenless/issues/12)) ([25fff4b](https://github.com/inference-gateway/tokenless/commit/25fff4bc8da2fb89caf5934816146f48636f3075))

### 👷 CI

* fix debug value format in tasks.yml ([f808112](https://github.com/inference-gateway/tokenless/commit/f808112578aaed304ef4d6f7f8e7e850d248c22c))
* sync OpenTask Agent workflow ([#14](https://github.com/inference-gateway/tokenless/issues/14)) ([c48f841](https://github.com/inference-gateway/tokenless/commit/c48f841f66bc8a1cb17392cfd7ec778948990a38))
* sync OpenTask Agent workflow ([#19](https://github.com/inference-gateway/tokenless/issues/19)) ([e90c58e](https://github.com/inference-gateway/tokenless/commit/e90c58e7488b600e63bcaaecc31ff379cf3e85ce))

### 🔧 Miscellaneous

* add dependabot config for Go module updates ([#22](https://github.com/inference-gateway/tokenless/issues/22)) ([3b9463d](https://github.com/inference-gateway/tokenless/commit/3b9463d1c6ccaee25922a979405be5a5f3c61f12))
* **ci:** update action versions to match release.yml (explicit) ([#20](https://github.com/inference-gateway/tokenless/issues/20)) ([0199ca5](https://github.com/inference-gateway/tokenless/commit/0199ca57989fd7f87dfb8b79fe2e1ca34291df2a))
* mark .githooks as vendored in .gitattributes ([#21](https://github.com/inference-gateway/tokenless/issues/21)) ([96709a1](https://github.com/inference-gateway/tokenless/commit/96709a12088e72e3265bf147679b2ff658342e05))

## [0.2.0](https://github.com/inference-gateway/tokenless/compare/v0.1.2...v0.2.0) (2026-08-05)

### ✨ Features

* add OpenTask Agent workflow ([#3](https://github.com/inference-gateway/tokenless/issues/3)) ([688bc79](https://github.com/inference-gateway/tokenless/commit/688bc79300a02d9b0fa4d96cd74e4d8677dc2953))
* **gateway:** return modalities in /v1/models response ([#9](https://github.com/inference-gateway/tokenless/issues/9)) ([a40f3d9](https://github.com/inference-gateway/tokenless/commit/a40f3d905733fb9e6082a0fbff36f82ac08f2809))

### 🐛 Bug Fixes

* **ci:** syntax for getting GitHub App User ID ([dc9e37f](https://github.com/inference-gateway/tokenless/commit/dc9e37f55fac0176928b068f9426fcbe555c0db1))

### 👷 CI

* **release:** use create-github-app-token for releaser bot authentication ([#6](https://github.com/inference-gateway/tokenless/issues/6)) ([b42dc36](https://github.com/inference-gateway/tokenless/commit/b42dc362aa0dbd9d2ec745faf36eb726bc193d3e))
* sync OpenTask Agent workflow ([#10](https://github.com/inference-gateway/tokenless/issues/10)) ([b448283](https://github.com/inference-gateway/tokenless/commit/b4482839d4db1711835e7c2e7678ed17e9cddb9e))
* sync OpenTask Agent workflow ([#7](https://github.com/inference-gateway/tokenless/issues/7)) ([a375173](https://github.com/inference-gateway/tokenless/commit/a375173fcc16bd244e77abda45b07963f017f0a5))

### 📚 Documentation

* add AGENTS.md, pre-commit hook, and agent symlinks ([#4](https://github.com/inference-gateway/tokenless/issues/4)) ([1bec5f3](https://github.com/inference-gateway/tokenless/commit/1bec5f35496c1c492ca095d4f96fd2876efb7a2d))
* add tokenless agent skill ([#1](https://github.com/inference-gateway/tokenless/issues/1)) ([aec6534](https://github.com/inference-gateway/tokenless/commit/aec6534efc671bb010595e4fd5d479529f16798a))

### 🔧 Miscellaneous

* add semantic-release configuration ([#5](https://github.com/inference-gateway/tokenless/issues/5)) ([1c6edd7](https://github.com/inference-gateway/tokenless/commit/1c6edd768db0bfc8b7c2d82dc0a6285ab3f146fc))
