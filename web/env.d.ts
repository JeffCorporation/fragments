/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, never>, Record<string, never>, unknown>
  export default component
}

declare module 'justified-layout' {
  export interface JLBox {
    width: number
    height: number
    top: number
    left: number
    aspectRatio: number
  }
  export interface JLResult {
    containerHeight: number
    widowCount: number
    boxes: JLBox[]
  }
  export interface JLOptions {
    containerWidth?: number
    containerPadding?: number | { top: number; right: number; bottom: number; left: number }
    boxSpacing?: number | { horizontal: number; vertical: number }
    targetRowHeight?: number
    targetRowHeightTolerance?: number
    showWidows?: boolean
  }
  export default function justifiedLayout(
    input: Array<{ width: number; height: number }>,
    options?: JLOptions,
  ): JLResult
}
