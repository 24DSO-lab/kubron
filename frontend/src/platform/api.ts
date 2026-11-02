// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Единственное место, где мы обращаемся к бэкенду по имени метода (ADR-0010).
 * Транспорт выбирается при сборке (VITE_TARGET): десктоп — Go-биндинги Wails,
 * веб — POST /api/rpc/{method} (rpc.web.ts).
 *
 * Имя метода и аргументы проверяются по wailsjs/go/main/App.d.ts: опечатка в имени
 * или неверное число/тип аргументов ловится в `vue-tsc`, а не в рантайме.
 * Динамический импорт и проверка наличия функции оставлены на случай устаревших биндингов.
 */
import { NOT_AUTHENTICATED_MESSAGE } from '../i18n/backendErrors'

type AppApi = typeof import('../../wailsjs/go/main/App')

/** Имена методов Go-приложения, доступные из фронтенда */
export type AppMethod = {
  [K in keyof AppApi]: AppApi[K] extends (...args: any[]) => Promise<any> ? K : never
}[keyof AppApi]

/** Текст ошибки бэкенда «нет сессии» (auth.ErrNotAuthenticated), см. i18n/backendErrors.ts */
export const NOT_AUTHENTICATED = NOT_AUTHENTICATED_MESSAGE

/** Методы, для которых «нет сессии» — штатный ответ (до входа), а не признак истёкшей сессии */
const PRE_LOGIN_METHODS = new Set<string>(['AuthStatus', 'SetupAdmin', 'Login', 'Logout', 'CurrentUser'])

let sessionLostHandler: (() => void) | null = null

/** Обработчик потери сессии на стороне бэкенда (автовыход по бездействию и т.п.) */
export function onSessionLost(handler: () => void) {
  sessionLostHandler = handler
}

/**
 * Токен первичной настройки для следующего SetupAdmin (только веб; сервисный режим). В десктопе токена нет.
 * Передаётся заголовком X-Kubron-Setup-Token, поэтому сигнатура SetupAdmin и привязки Wails не меняются.
 */
export async function setSetupToken(token: string): Promise<void> {
  if (import.meta.env.VITE_TARGET !== 'web') return
  const { setSetupToken: set } = await import('./rpc.web')
  set(token)
}

export async function callApp<K extends AppMethod>(
  fn: K,
  ...args: Parameters<AppApi[K]>
): Promise<Awaited<ReturnType<AppApi[K]>>> {
  try {
    return await invoke(String(fn), args) as Awaited<ReturnType<AppApi[K]>>
  } catch (e) {
    const text = typeof e === 'string' ? e : (e as { message?: string } | null)?.message ?? ''
    if (text.includes(NOT_AUTHENTICATED) && !PRE_LOGIN_METHODS.has(String(fn))) sessionLostHandler?.()
    throw e
  }
}

async function invoke(fn: string, args: unknown[]): Promise<unknown> {
  // VITE_TARGET подставляется при сборке: ветка другой оболочки вырезается из бандла
  if (import.meta.env.VITE_TARGET === 'web') {
    const { rpc } = await import('./rpc.web')
    return rpc(fn, args)
  }
  const app: any = await import('../../wailsjs/go/main/App')
  if (typeof app[fn] !== 'function') {
    throw new Error(`${fn} is not available in the Go bindings`)
  }
  return app[fn](...args)
}
