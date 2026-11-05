// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import {
  challengeFields, clearFieldValues, commandLine, fieldsFilled, formatCountdown, isMultilineField, isWaiting, pollIntervalMs, safeHref, secondsLeft, shellQuoteWord,
} from './challengeView'
import type { Challenge } from '../../types/credential'

const ch = (p: Partial<Challenge>): Challenge => ({ id: 'c1', kind: 'credentials', expiresAt: '2030-01-01T00:00:00Z', ...p })

describe('challengeFields', () => {
  it('credentials: по умолчанию логин и секретный пароль', () => {
    expect(challengeFields(ch({}))).toEqual([{ name: 'username', secret: false }, { name: 'password', secret: true }])
  })
  it('paste-redirect и manual-token имеют фиксированное поле', () => {
    expect(challengeFields(ch({ kind: 'paste-redirect' }))).toEqual([{ name: 'redirectUrl', secret: false }])
    expect(challengeFields(ch({ kind: 'manual-token' }))).toEqual([{ name: 'token', secret: true }])
  })
  it('secret-fields: поля провайдера как есть', () => {
    const f = [{ name: 'accessKeyId', secret: false }, { name: 'secretAccessKey', secret: true }]
    expect(challengeFields(ch({ kind: 'secret-fields', fields: f }))).toEqual(f)
  })
  it('ожидающие виды без полей', () => {
    expect(challengeFields(ch({ kind: 'device-code' }))).toEqual([])
    expect(isWaiting(ch({ kind: 'device-code' }))).toBe(true)
    expect(isWaiting(ch({ kind: 'credentials' }))).toBe(false)
  })
})

describe('fieldsFilled', () => {
  const f = [{ name: 'u', secret: false }, { name: 'p', secret: true }]
  it('пробелы значимы только в секретах', () => {
    expect(fieldsFilled(f, { u: ' ', p: 'x' })).toBe(false)
    expect(fieldsFilled(f, { u: 'a', p: ' ' })).toBe(true)
    expect(fieldsFilled(f, { u: 'a', p: '' })).toBe(false)
    expect(fieldsFilled([], {})).toBe(false)
  })
})

describe('время', () => {
  it('secondsLeft не уходит ниже нуля, плохая дата — истёкшая', () => {
    const now = Date.parse('2030-01-01T00:00:00Z')
    expect(secondsLeft('2030-01-01T00:01:30Z', now)).toBe(90)
    expect(secondsLeft('2029-01-01T00:00:00Z', now)).toBe(0)
    expect(secondsLeft('nonsense', now)).toBe(0)
  })
  it('formatCountdown', () => {
    expect(formatCountdown(90)).toBe('1:30')
    expect(formatCountdown(5)).toBe('0:05')
    expect(formatCountdown(-3)).toBe('0:00')
  })
  it('pollIntervalMs ограничивает интервал', () => {
    expect(pollIntervalMs(undefined)).toBe(5000)
    expect(pollIntervalMs(0)).toBe(5000)
    expect(pollIntervalMs(2)).toBe(2000)
    expect(pollIntervalMs(9999)).toBe(60000)
  })
})

describe('показ команды', () => {
  it('безопасные слова без кавычек, остальные в одинарных', () => {
    expect(shellQuoteWord('aws')).toBe('aws')
    expect(shellQuoteWord('')).toBe("''")
    expect(shellQuoteWord('a b')).toBe("'a b'")
    expect(shellQuoteWord("it's")).toBe(`'it'\\''s'`)
    expect(shellQuoteWord('$(rm -rf /)')).toBe("'$(rm -rf /)'")
  })
  it('commandLine', () => {
    expect(commandLine(['aws', 'eks', 'get-token', '--cluster-name', 'my cluster'])).toBe("aws eks get-token --cluster-name 'my cluster'")
    expect(commandLine(undefined)).toBe('')
  })
})

describe('safeHref', () => {
  it('только http и https', () => {
    expect(safeHref('https://idp.example/auth')).toBe('https://idp.example/auth')
    expect(safeHref('javascript:alert(1)')).toBe('')
    expect(safeHref('data:text/html,x')).toBe('')
    expect(safeHref('not a url')).toBe('')
    expect(safeHref(undefined)).toBe('')
  })
})

describe('isMultilineField', () => {
  it('только явный список полей и только для secret-fields', () => {
    expect(isMultilineField('secret-fields', 'serviceAccountJson')).toBe(true)
    expect(isMultilineField('secret-fields', 'secretAccessKey')).toBe(false)
    expect(isMultilineField('secret-fields', 'unknownField')).toBe(false)
    expect(isMultilineField('credentials', 'serviceAccountJson')).toBe(false)
  })
})

describe('clearFieldValues', () => {
  const fields = [{ name: 'user', secret: false }, { name: 'serviceAccountJson', secret: true }]
  it('onlySecrets очищает секретные поля и оставляет остальные', () => {
    const values = { user: 'alice', serviceAccountJson: '{\n"k":1\n}' }
    expect(clearFieldValues(fields, values, true)).toBe(1)
    expect(values).toEqual({ user: 'alice', serviceAccountJson: '' })
  })
  it('без onlySecrets очищает всё', () => {
    const values = { user: 'alice', serviceAccountJson: 'x' }
    expect(clearFieldValues(fields, values, false)).toBe(2)
    expect(values).toEqual({ user: '', serviceAccountJson: '' })
  })
  it('заводит значения для полей, которых ещё нет', () => {
    const values: Record<string, string> = {}
    clearFieldValues(fields, values, false)
    expect(values).toEqual({ user: '', serviceAccountJson: '' })
  })
})
