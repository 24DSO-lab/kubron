# Выпуск релизов

Релиз выпускается в два шага:

```bash
scripts/release.sh X.Y.Z --prepare   # в рабочей ветке, до слияния в dev
scripts/release.sh X.Y.Z             # из dev, когда dev зелёный
```

Полный процесс, настройки GitHub и устранение неполадок описаны в [docs/releasing.md](https://github.com/24DSO-lab/kubron/blob/master/docs/releasing.md).

Изменения, заметные пользователям, добавляйте в `CHANGELOG.md` в раздел `[Unreleased]`.
