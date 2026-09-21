/*
 * 只是为了让 module.json5 的 `pages: "$profile:main_pages"` 指向一个真实存在的文件。
 * LoopProbeAbility 是刻意「无窗口」的：它不调 loadContent，探针结果只走 hilog。
 * 加 UI 只会多一条可能失败、且与结论无关的路径。
 */
@Entry
@Component
struct Index {
  build() {
    Column() {
      Text('LoopProbe').fontSize(28).fontWeight(FontWeight.Bold)
      Text('结果在 hilog（tag = LoopProbe）').fontSize(14).margin({ top: 12 })
    }
    .width('100%')
    .height('100%')
    .justifyContent(FlexAlign.Center)
  }
}
