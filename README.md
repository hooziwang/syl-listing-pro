# syl-listing-pro

面向亚马逊 Listing 的双语生成 CLI。

输入一份需求 Markdown，输出：
- 英文 Listing（`.md`）
- 中文 Listing（`.md`）
- 英文 Word（`.docx`，自动高亮关键词）
- 中文 Word（`.docx`，自动高亮关键词）

## 特性

- 直跑命令：`syl-listing-pro <file_or_dir ...>`
- 自动双语生成：英文生成 + 中文翻译
- 自动转 Word：生成完成后自动调用 `syl-md2doc`
- 同名产物：`*_en.md -> *_en.docx`，`*_cn.md -> *_cn.docx`
- 规则自动同步：每次运行自动检查规则更新
- 输出友好：默认人类可读进度；`--verbose` 输出 NDJSON（机器友好）

## 安装

### macOS（Homebrew）

```bash
brew update && brew install hooziwang/tap/syl-listing-pro
```

说明：会自动安装依赖 `syl-md2doc` 和 `pandoc`。

升级：

```bash
brew update && brew upgrade hooziwang/tap/syl-listing-pro
```

### Windows（Scoop）

首次使用（本机还没有 Scoop）：

```powershell
Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
iwr -useb get.scoop.sh | iex
```

安装后请关闭并重新打开 PowerShell，然后验证：

```powershell
scoop --version
```

如果提示 `无法将“scoop”项识别为 cmdlet`，先把 Scoop shims 加入用户 PATH，再重开终端：

```powershell
[Environment]::SetEnvironmentVariable("Path", $env:Path + ";$env:USERPROFILE\\scoop\\shims", "User")
```

安装 `syl-listing-pro`：

```powershell
scoop update
scoop bucket add hooziwang https://github.com/hooziwang/scoop-bucket.git
scoop install syl-listing-pro
```

说明：会自动安装依赖 `syl-md2doc` 和 `pandoc`。

升级：

```powershell
scoop update; scoop update syl-listing-pro
```

### 从 Release 二进制安装

从 Releases 下载对应平台压缩包，解压后将 `syl-listing-pro` 加入 PATH。
如果是手动安装二进制，还需要自行安装依赖：
- `syl-md2doc`
- `pandoc`

## 快速开始

### 1) 配置 Key（只做一次）

```bash
syl-listing-pro set key <SYL_LISTING_KEY>
```

说明：
- Key 保存在 `~/.syl-listing-pro/.env`
- 命令成功时不输出任何内容

### 2) 准备需求 Markdown

使用官方模板填写需求内容，首行标记需与当前规则版本一致。

如果格式不匹配，CLI 会提示类似：
- `未发现 listing 要求文件`
- `规则中未定义输入识别标记`

### 3) 运行生成

```bash
syl-listing-pro /abs/path/pinpai.md
```

或目录批量：

```bash
syl-listing-pro /abs/path/requirements_dir
```

## 命令

### 生成（默认命令）

```bash
syl-listing-pro [file_or_dir ...] [--out ...] [-n ...] [--verbose] [--log-file ...]
```

等价：

```bash
syl-listing-pro gen [file_or_dir ...]
```

### 设置 Key

```bash
syl-listing-pro set key <SYL_LISTING_KEY>
```

### 强制更新规则

```bash
syl-listing-pro update rules
```

行为：清除本地规则缓存并下载最新规则。

### 版本

```bash
syl-listing-pro -v
# 或
syl-listing-pro version
```

## 常用参数

- `-o, --out`：输出目录（默认当前目录）
- `-n, --num`：每个需求文件生成候选数量（默认 `1`）
- `--verbose`：输出 NDJSON 详细日志（含 worker 事件）
- `--log-file`：将日志同时写入文件

## 输出规则

每个任务成功后会产生 4 个文件：

- `listing_<id>_en.md`
- `listing_<id>_cn.md`
- `listing_<id>_en.docx`
- `listing_<id>_cn.docx`

其中 `<id>` 为本次任务识别码。

## 日志模式

### 默认模式（人类友好）

示例：

```text
syl:00:00 任务已加入队列 job_xxx
syl:00:00 规则已加载 rules-xxx
syl:00:44 EN 已写入：/abs/listing_xxx_en.md
syl:00:44 CN 已写入：/abs/listing_xxx_cn.md
syl:00:44 EN Word 已写入：/abs/listing_xxx_en.docx
syl:00:44 CN Word 已写入：/abs/listing_xxx_cn.docx
任务完成：成功 1，失败 0，总耗时 48s
```

### `--verbose` 模式（机器友好）

输出 NDJSON，每行一个 JSON 事件，便于脚本解析和链路排障。

## 数据位置

- Key：`~/.syl-listing-pro/.env`
- 规则缓存：系统缓存目录（由程序自动管理）

说明：
- 规则对用户默认不可见，不需要手工编辑。
- CLI 不提供可配置 `base_url`，固定连接服务端。

## 常见问题

1. `尚未配置 KEY，需要执行 syl-listing-pro set key <SYL_LISTING_KEY>`
先执行 `set key`。

2. `规则中心不可达且首次运行无缓存`
当前网络无法访问服务端且本地无规则缓存。恢复网络后重试。

3. `EN/CN Word 转换失败`
优先检查：
- `syl-md2doc` 是否可执行
- `pandoc` 是否可执行

4. 文件被识别失败（未发现需求文件）
说明输入文件首行标记与当前规则不匹配，改用最新模板。

## 退出码

- 全部成功：`0`
- 存在失败任务：`1`
