# syl-listing-pro

新一代 listing CLI（Go + Cobra）。

## 命令

- `syl-listing-pro gen [file_or_dir ...]`
- `syl-listing-pro [file_or_dir ...]`（直跑 `gen`）
- `syl-listing-pro update rules`
- `syl-listing-pro set key <SYL_LISTING_KEY>`
- `syl-listing-pro version`

## 规则文件识别

首行必须是：

```text
===Listing Requirements===
```

## 构建

```bash
make
```
