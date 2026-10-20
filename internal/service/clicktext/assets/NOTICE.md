# 字形图集来源

`glyphs.png` 由 `go run ./tools/glyphatlas` 从 **Noto Sans SC Regular**（思源黑体简体）生成，包含 `charset.go` 中列出的 336 个常用字。

- 字体来源：<https://github.com/notofonts/noto-cjk>（`Sans/SubsetOTF/SC/NotoSansSC-Regular.otf`）
- 许可：SIL Open Font License 1.1，全文见同目录 `LICENSE-OFL.txt`。OFL 允许把字体渲染成位图后随软件分发。
- 字体文件本身不提交到仓库：放在 `third_party/fonts/`，已被 `.gitignore` 忽略。修改字表后需要重新生成图集。
