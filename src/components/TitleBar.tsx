import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { Minus, Square, X } from 'lucide-react'
import clsx from 'clsx'
import { api } from '../lib/api'

/**
 * Frameless window chrome. Drag region + min/max/close.
 * Matches light Windsurf marketing aesthetic.
 * When settings.closeToTray is on, close hides the window (tray keeps app alive).
 */
export function TitleBar() {
  const [maximized, setMaximized] = useState(false)

  const refreshMax = useCallback(async () => {
    try {
      setMaximized(await getCurrentWindow().isMaximized())
    } catch {
      /* not in tauri */
    }
  }, [])

  useEffect(() => {
    refreshMax()
    let un: (() => void) | undefined
    ;(async () => {
      try {
        const win = getCurrentWindow()
        un = await win.onResized(() => {
          void refreshMax()
        })
      } catch {
        /* ignore */
      }
    })()
    return () => {
      un?.()
    }
  }, [refreshMax])

  const minimize = async () => {
    try {
      await getCurrentWindow().minimize()
    } catch {
      /* ignore */
    }
  }

  const toggleMax = async () => {
    try {
      await getCurrentWindow().toggleMaximize()
      await refreshMax()
    } catch {
      /* ignore */
    }
  }

  const close = async () => {
    try {
      // Prefer hide-to-tray when enabled (backend also intercepts CloseRequested).
      let closeToTray = false
      try {
        const s = await api.getSettings()
        closeToTray = Boolean(s.closeToTray)
      } catch {
        /* ignore */
      }
      const win = getCurrentWindow()
      if (closeToTray) {
        await win.hide()
      } else {
        await win.close()
      }
    } catch {
      /* ignore */
    }
  }

  return (
    <header
      className="h-11 shrink-0 flex items-center select-none border-b border-zinc-200/90 bg-white/90 backdrop-blur-md z-50"
      data-tauri-drag-region
    >
      {/* Brand / drag */}
      <div
        className="flex-1 h-full flex items-center gap-2.5 pl-3 min-w-0"
        data-tauri-drag-region
        onDoubleClick={() => void toggleMax()}
      >
        <img
          src="/app-icon.png"
          alt=""
          width={18}
          height={18}
          className="rounded-[4px] shadow-sm pointer-events-none"
          draggable={false}
        />
        <span
          className="text-[13px] font-semibold tracking-tight text-zinc-800 truncate pointer-events-none"
          data-tauri-drag-region
        >
          opencode2api 管理器
        </span>
      </div>

      {/* Window controls — no drag so clicks work */}
      <div className="flex h-full items-stretch shrink-0">
        <WinBtn aria-label="最小化" onClick={() => void minimize()}>
          <Minus size={14} strokeWidth={2} />
        </WinBtn>
        <WinBtn aria-label={maximized ? '还原' : '最大化'} onClick={() => void toggleMax()}>
          {maximized ? (
            <span className="relative inline-block w-[12px] h-[12px]">
              <span className="absolute left-0 bottom-0 w-[9px] h-[9px] border-[1.5px] border-current bg-white" />
              <span className="absolute right-0 top-0 w-[9px] h-[9px] border-[1.5px] border-current bg-transparent" />
            </span>
          ) : (
            <Square size={12} strokeWidth={2} />
          )}
        </WinBtn>
        <WinBtn aria-label="关闭" danger onClick={() => void close()}>
          <X size={14} strokeWidth={2} />
        </WinBtn>
      </div>
    </header>
  )
}

function WinBtn({
  children,
  onClick,
  danger,
  'aria-label': ariaLabel,
}: {
  children: ReactNode
  onClick: () => void
  danger?: boolean
  'aria-label': string
}) {
  return (
    <button
      type="button"
      aria-label={ariaLabel}
      onClick={onClick}
      className={clsx(
        'w-[46px] h-full grid place-items-center transition-colors duration-150',
        danger
          ? 'text-zinc-600 hover:bg-red-500 hover:text-white'
          : 'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900',
      )}
    >
      {children}
    </button>
  )
}