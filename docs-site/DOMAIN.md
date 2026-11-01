# Бесплатный (и почти бесплатный) домен для docs

## Вариант 1 — полностью бесплатно (рекомендуется на старте)

### GitHub Pages subdomain

После деплоя workflow сайт будет доступен по адресу:

```
https://24dso-labs.github.io/kubron/
```

Ничего покупать не нужно. Это самый простой и надёжный вариант.

В workflow уже выставлен `DOCS_BASE: /kubron/`.

---

### Cloudflare Pages (тоже бесплатно)

1. Зарегистрируйся на [dash.cloudflare.com](https://dash.cloudflare.com) (бесплатный аккаунт).
2. Workers & Pages → Create → Connect to Git → выбери репозиторий `kubron`.
3. Build settings:
   - Framework preset: VitePress (или None)
   - Build command: `cd docs-site && npm ci && npm run docs:build`
   - Build output directory: `docs-site/.vitepress/dist`
   - Environment variable: `DOCS_BASE=/`
4. Получишь адрес вида:

```
https://kubron-xxxx.pages.dev
```

Можно сразу задать красивое имя проекта `kubron` → `https://kubron.pages.dev` (если свободно).

Плюсы Cloudflare Pages:
- Быстрее GitHub Pages в большинстве регионов
- Бесплатный SSL
- Легко потом привязать свой домен

---

## Вариант 2 — бесплатный / очень дешёвый кастомный домен

Настоящих «вечных» бесплатных доменов с хорошей репутацией почти не осталось (Freenom `.tk/.ml` сейчас ненадёжен и часто блокируется).

### Рекомендуемые дешёвые варианты

| Домен | Примерная цена | Где брать |
|---|---|---|
| `.dev` | ~12–15 $/год | Google Domains / Porkbun / Namecheap |
| `.io` | ~30–40 $/год | Porkbun / Namecheap |
| `.app` | ~12–15 $/год | Google / Porkbun |
| `.pages.dev` | бесплатно | Cloudflare Pages (см. выше) |

**Лучший практический путь:**

1. Сначала запусти сайт на `24dso-labs.github.io/kubron` или `kubron.pages.dev`.
2. Когда проект подрастёт — купи `kubron.dev` или `docs.kubron.dev` на Porkbun / Cloudflare Registrar (часто самые низкие цены).
3. Привяжи домен:

#### К GitHub Pages

- Settings → Pages → Custom domain → впиши `docs.kubron.dev`
- Добавь DNS-записи у регистратора (GitHub покажет какие).
- Включи «Enforce HTTPS».
- В workflow поменяй `DOCS_BASE` на `/`.

#### К Cloudflare Pages

- В проекте Pages → Custom domains → Add → `docs.kubron.dev`
- Cloudflare сам настроит DNS и SSL (если домен на Cloudflare).

---

## Что я рекомендую сделать прямо сейчас

1. Положи `docs-site/` в репозиторий.
2. Добавь workflow.
3. Включи GitHub Pages → получи `https://24dso-labs.github.io/kubron/`.
4. (Опционально) Параллельно задеплой на Cloudflare Pages → `kubron.pages.dev`.
5. В README репозитория добавь ссылку:

```md
📖 [Documentation](https://24dso-labs.github.io/kubron)
```

Когда будешь готов к своему домену — бери `kubron.dev` (или `getkubron.dev` / `kubron.app`) на Porkbun/Cloudflare и привязывай к Pages.
