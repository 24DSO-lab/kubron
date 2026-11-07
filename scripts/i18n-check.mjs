#!/usr/bin/env node
// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Проверка локализации без внешних зависимостей: `npm run i18n:check` (из frontend/) или
 * `node scripts/i18n-check.mjs`. В CI — отдельный шаг до сборки.
 *
 * Ошибки (код возврата 1):
 *  - файл локали не парсится или не объект;
 *  - в языке нет ключей, которые есть в en, или есть лишние;
 *  - в переводе другой набор плейсхолдеров {name}, чем в en;
 *  - в коде t('a.b') / $t('a.b') / <i18n-t keypath> ссылается на ключ, которого нет в en;
 *  - t('Какой-то текст') — вместо ключа передан текст;
 *  - errors.New/fmt.Errorf в internal/ и pkg/ без записи в backend.json;
 *  - кириллица в исходниках вне комментариев (UI-тексты живут только в locales/).
 *
 * Предупреждения (с --strict тоже ошибки):
 *  - английский литерал в <template> (похоже на непереведённый текст);
 *  - ключ из en нигде не используется.
 */
import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const src = join(root, 'frontend', 'src')
const localesDir = join(src, 'i18n', 'locales')
const BASE = 'en'
const strict = process.argv.includes('--strict')

/** Литералы, которые не переводятся (бренды, технические термины) */
const ALLOWED_LITERALS = new Set([
  'Kubron', 'Kubernetes', 'K8s', 'Argo CD', 'ArgoCD', 'Helm', 'YAML', 'JSON', 'kubectl', 'kubeconfig',
  'Pod', 'Pods', 'Deployment', 'Deployments', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob',
  'Service', 'Services', 'Ingress', 'ConfigMap', 'Secret', 'Secrets', 'Node', 'Nodes', 'Namespace',
  'CPU', 'RAM', 'OK', 'URL', 'ID', 'UID', 'IP', 'TCP', 'UDP', 'HTTP', 'HTTPS', 'TLS', 'SSO', 'OIDC',
  // Flux CD: бренд и имена объектов (ADR-0023)
  'Flux', 'Flux CD', 'Kustomization', 'HelmRelease', 'GitRepository', 'OCIRepository', 'HelmRepository', 'HelmChart',
  'Bucket', 'Alert', 'Provider', 'Receiver', 'ImageRepository', 'ImagePolicy', 'ImageUpdateAutomation',
])

const errors = []
const warnings = []
const rel = (p) => relative(root, p).split('\\').join('/')

function walk(dir, accept, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name === 'node_modules' || entry.name === 'wailsjs') continue
      walk(full, accept, out)
    } else if (accept(full)) out.push(full)
  }
  return out
}

// ---------- 1. локали ----------

function flatten(value, prefix, out, file) {
  for (const [key, v] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof v === 'string') out.set(path, v)
    else if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, path, out, file)
    else errors.push(`${rel(file)}: «${path}» — значение должно быть строкой или объектом`)
  }
}

function loadLocale(code) {
  const flat = new Map()
  const dir = join(localesDir, code)
  for (const name of readdirSync(dir).filter((f) => f.endsWith('.json')).sort()) {
    const file = join(dir, name)
    let json
    try {
      json = JSON.parse(readFileSync(file, 'utf8'))
    } catch (e) {
      errors.push(`${rel(file)}: невалидный JSON (${e.message})`)
      continue
    }
    if (!json || typeof json !== 'object' || Array.isArray(json)) {
      errors.push(`${rel(file)}: корень должен быть объектом`)
      continue
    }
    flatten(json, name.replace(/\.json$/, ''), flat, file)
  }
  return flat
}

const placeholders = (s) => new Set([...s.matchAll(/\{([A-Za-z_]\w*)\}/g)].map((m) => m[1]))
const linked = (s) => [...s.matchAll(/@(?:\.\w+)?:([\w.]+)/g)].map((m) => m[1])

const codes = readdirSync(localesDir, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name)
if (!codes.includes(BASE)) errors.push(`нет базового языка: ${rel(join(localesDir, BASE))}`)
const locales = new Map(codes.map((c) => [c, loadLocale(c)]))
const base = locales.get(BASE) ?? new Map()

for (const [code, flat] of locales) {
  if (code === BASE) continue
  const missing = [...base.keys()].filter((k) => !flat.has(k))
  const extra = [...flat.keys()].filter((k) => !base.has(k))
  if (missing.length) errors.push(`${code}: не хватает ${missing.length} ключей: ${missing.slice(0, 8).join(', ')}${missing.length > 8 ? ', …' : ''}`)
  if (extra.length) errors.push(`${code}: лишние ключи (нет в en): ${extra.slice(0, 8).join(', ')}${extra.length > 8 ? ', …' : ''}`)
  for (const [key, text] of flat) {
    const en = base.get(key)
    if (en === undefined) continue
    const a = [...placeholders(en)].sort().join(',')
    const b = [...placeholders(text)].sort().join(',')
    if (a !== b) errors.push(`${code}: «${key}» — плейсхолдеры {${b}} не совпадают с en {${a}}`)
    if (text.trim() === '') errors.push(`${code}: «${key}» — пустой перевод`)
  }
}
for (const [code, flat] of locales) {
  for (const [key, text] of flat) {
    for (const ref of linked(text)) {
      if (!flat.has(ref)) errors.push(`${code}: «${key}» ссылается на несуществующий ключ @:${ref}`)
    }
  }
}

// ---------- 2. исходники ----------

const blank = (m) => m.replace(/[^\n]/g, ' ') // убрать текст, сохранив номера строк

function stripComments(text) {
  return text
    .replace(/<!--[\s\S]*?-->/g, blank)
    .replace(/\/\*[\s\S]*?\*\//g, blank)
    .replace(/(^|[^:'"`\\])\/\/[^\n]*/g, (m, p1) => p1 + blank(m.slice(p1.length)))
}

const lineOf = (text, index) => text.slice(0, index).split('\n').length

const sources = walk(src, (f) => /\.(vue|ts)$/.test(f) && !/\.test\.ts$/.test(f) && !f.includes(`${join('i18n', 'locales')}`))
  .filter((f) => !f.endsWith('languages.ts')) // там названия языков на их языках
  .filter((f) => !f.endsWith('vite-env.d.ts'))

const KEY = /^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_-]+)+$/
const usedKeys = new Set()
const usedPrefixes = new Set(['backend.']) // ключи backend.* выбираются по тексту ошибки (i18n/backendErrors.ts)

const callRe = /(?<![\w.$])\$?(?:t|tl|te)\(\s*(?:(['"])([^'"\n]*)\1|`([^`]*)`)/g
const keypathRe = /\bkeypath=(?:"([^"]+)"|'([^']+)')|:keypath="'([^']+)'"/g

for (const file of sources) {
  const raw = readFileSync(file, 'utf8')
  const text = stripComments(raw)
  const isVue = file.endsWith('.vue')
  const code = isVue ? text.replace(/<style[\s\S]*?<\/style>/g, blank) : text

  for (const m of code.matchAll(callRe)) {
    const line = lineOf(code, m.index)
    if (m[3] !== undefined) {
      const prefix = m[3].split('${')[0]
      if (m[3].includes('${')) usedPrefixes.add(prefix)
      else check(file, line, m[3])
    } else check(file, line, m[2])
  }
  if (isVue) {
    for (const m of code.matchAll(keypathRe)) check(file, lineOf(code, m.index), m[1] ?? m[2] ?? m[3])
  }

  // ключи в тернарниках и массивах: t(cond ? 'a.b' : 'c.d') — считаем использованными любые литералы-ключи
  for (const m of code.matchAll(/(['"])([A-Za-z0-9_]+(?:\.[A-Za-z0-9_-]+)+)\1/g)) if (base.has(m[2])) usedKeys.add(m[2])

  // кириллица вне комментариев
  const noStyle = isVue ? code : text
  noStyle.split('\n').forEach((l, i) => {
    if (/[\u0400-\u04FF]/.test(l)) errors.push(`${rel(file)}:${i + 1}: кириллица в коде — вынесите в locales/: ${l.trim().slice(0, 90)}`)
  })

  if (isVue) scanTemplate(file, code)
}

function check(file, line, key) {
  if (!KEY.test(key)) {
    errors.push(`${rel(file)}:${line}: «${key}» — это не ключ сообщения (ожидается вид section.name)`)
    return
  }
  usedKeys.add(key)
  if (!base.has(key)) errors.push(`${rel(file)}:${line}: ключ «${key}» отсутствует в en`)
}

function scanTemplate(file, code) {
  const start = code.indexOf('<template')
  const end = code.lastIndexOf('</template>')
  if (start < 0 || end < 0) return
  const tpl = code.slice(start, end).replace(/\{\{[\s\S]*?\}\}/g, blank)
  const report = (index, text) => {
    const t = text.replace(/&\w+;/g, ' ').replace(/\s+/g, ' ').trim()
    if (!/[A-Za-z]{2,}/.test(t) || ALLOWED_LITERALS.has(t)) return
    warnings.push(`${rel(file)}:${lineOf(code, start + index)}: английский литерал в шаблоне: «${t.slice(0, 70)}»`)
  }
  for (const m of tpl.matchAll(/>([^<>]+)</g)) report(m.index + 1, m[1])
  for (const m of tpl.matchAll(/(?<![:@\w-])(?:title|placeholder|aria-label|alt|label)="([^"]*[A-Za-z]{2,}[^"]*)"/g)) report(m.index, m[1])
  for (const m of tpl.matchAll(/:(?:title|placeholder|aria-label|alt|label)="'([^']*[A-Za-z]{2,}[^']*)'"/g)) report(m.index, m[1])
}

// ---------- 2b. ошибки Go и таблица перевода backend.* ----------

/** Go-формат → шаблон таблицы: %s/%d/%q/%v/%w по порядку → {p0}, {p1}… */
function goTemplate(format) {
  let n = 0
  return format
    .replace(/\\"/g, '"')
    .replace(/\\n/g, '\n')
    .replace(/\\\\/g, '\\')
    .replace(/%[-+# 0-9.*]*[a-zA-Z%]/g, (m) => (m === '%%' ? '%' : `{p${n++}}`))
}
{
  const unescapeVue = (v) => v.replace(/\{'(.)'\}/g, '$1')
  const known = new Set([...base].filter(([k]) => k.startsWith('backend.')).map(([, v]) => unescapeVue(v)))
  // Сообщение целиком из чужого текста: переводить нечего
  const passthrough = new Set(['{p0}', '{p0}: {p1}', '{p0} {p1}: {p2}', '{p0}: HTTP {p1}', '{p0}: {p1}_{p2}'])
  const goRoots = ['internal', 'pkg'].map((d) => join(root, d))
  const missing = new Map()
  for (const dir of goRoots) {
    let files = []
    try {
      files = walk(dir, (f) => f.endsWith('.go') && !f.endsWith('_test.go'))
    } catch {
      continue
    }
    for (const file of files) {
      const text = readFileSync(file, 'utf8')
      for (const m of text.matchAll(/(?:errors\.New|fmt\.Errorf)\(\s*"((?:[^"\\]|\\.)*)"/g)) {
        const template = goTemplate(m[1])
        if (!known.has(template) && !passthrough.has(template)) missing.set(template, `${rel(file)}:${lineOf(text, m.index)}`)
      }
    }
  }
  for (const [template, where] of missing) {
    errors.push(`${where}: ошибка Go без записи в backend.json (en и ru): «${template.slice(0, 80)}»`)
  }
}

// ---------- 3. неиспользуемые ключи ----------

const unused = [...base.keys()].filter(
  (k) => !usedKeys.has(k) && ![...usedPrefixes].some((p) => k.startsWith(p)) && !isLinkedTarget(k),
)
function isLinkedTarget(key) {
  return [...base.values()].some((v) => linked(v).includes(key))
}
if (unused.length) warnings.push(`ключи en без видимых ссылок в коде: ${unused.length} (${unused.slice(0, 80).join(', ')}${unused.length > 80 ? ', …' : ''})`)

// ---------- итог ----------

const out = (title, list) => {
  if (!list.length) return
  console.log(`\n${title} (${list.length}):`)
  for (const l of list.slice(0, 200)) console.log('  ' + l)
  if (list.length > 200) console.log(`  … и ещё ${list.length - 200}`)
}
out('ОШИБКИ', errors)
out(strict ? 'ОШИБКИ (--strict)' : 'Предупреждения', warnings)
console.log(`\nЯзыки: ${codes.join(', ')}; ключей в en: ${base.size}; файлов проверено: ${sources.length}`)
const failed = errors.length > 0 || (strict && warnings.length > 0)
console.log(failed ? 'i18n-check: FAIL' : 'i18n-check: OK')
process.exit(failed ? 1 : 0)
