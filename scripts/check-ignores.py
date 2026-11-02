#!/usr/bin/env python3
"""Проверяет списки исключений сканеров уязвимостей: .trivyignore и osv-scanner.toml.

У каждой записи должен быть срок, не дальше MAX_DAYS от сегодняшнего дня, и обоснование (docs/security-review.md,
docs/releasing.md). Просроченная запись тоже ошибка: её нужно удалить или пересмотреть.
Использование: scripts/check-ignores.py [--today YYYY-MM-DD]
"""
import argparse
import datetime as dt
import pathlib
import re
import sys
import tomllib

MAX_DAYS = 180
ROOT = pathlib.Path(__file__).resolve().parent.parent


def check_trivyignore(path: pathlib.Path, today: dt.date) -> list[str]:
    errors: list[str] = []
    if not path.exists():
        return errors
    entry = re.compile(r"^(\S+)\s+exp:(\d{4}-\d{2}-\d{2})\s*$")
    prev_comment = False
    for no, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line:
            prev_comment = False
            continue
        if line.startswith("#"):
            # «Пример» в шапке не считается: комментарий-обоснование должен быть непустым
            prev_comment = len(line.lstrip("# ").strip()) >= 10
            continue
        m = entry.match(line)
        where = f"{path.name}:{no}"
        if not m:
            errors.append(f"{where}: запись «{line}» без срока; формат: ID exp:YYYY-MM-DD")
        else:
            try:
                exp = dt.date.fromisoformat(m.group(2))
            except ValueError:
                errors.append(f"{where}: неверная дата {m.group(2)}")
                exp = None
            if exp is not None:
                if exp < today:
                    errors.append(f"{where}: срок {exp} истёк, удалите запись или пересмотрите её")
                elif (exp - today).days > MAX_DAYS:
                    errors.append(f"{where}: срок {exp} дальше {MAX_DAYS} дней")
        if not prev_comment:
            errors.append(f"{where}: над записью нет комментария с обоснованием")
        prev_comment = False
    return errors


def check_osv(path: pathlib.Path, today: dt.date) -> list[str]:
    errors: list[str] = []
    if not path.exists():
        return errors
    data = tomllib.loads(path.read_text(encoding="utf-8"))
    for i, v in enumerate(data.get("IgnoredVulns", []), 1):
        where = f"{path.name}: IgnoredVulns #{i} ({v.get('id', '?')})"
        until = v.get("ignoreUntil")
        if until is None:
            errors.append(f"{where}: нет ignoreUntil")
        else:
            d = until.date() if isinstance(until, dt.datetime) else until
            if not isinstance(d, dt.date):
                errors.append(f"{where}: ignoreUntil не дата")
            elif d < today:
                errors.append(f"{where}: срок {d} истёк")
            elif (d - today).days > MAX_DAYS:
                errors.append(f"{where}: срок {d} дальше {MAX_DAYS} дней")
        if len(str(v.get("reason", "")).strip()) < 10:
            errors.append(f"{where}: нет reason с обоснованием")
    for i, v in enumerate(data.get("PackageOverrides", []), 1):
        if "ignore" in v and v["ignore"] and not (v.get("effectiveUntil") and len(str(v.get("reason", ""))) >= 10):
            errors.append(f"{path.name}: PackageOverrides #{i} с ignore требует effectiveUntil и reason")
    return errors


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--today", help="дата проверки для тестов, YYYY-MM-DD")
    args = ap.parse_args()
    today = dt.date.fromisoformat(args.today) if args.today else dt.date.today()
    errors = check_trivyignore(ROOT / ".trivyignore", today) + check_osv(ROOT / "osv-scanner.toml", today)
    for e in errors:
        print(f"ERROR: {e}", file=sys.stderr)
    if errors:
        return 1
    print("OK: списки исключений сканеров в порядке")
    return 0


if __name__ == "__main__":
    sys.exit(main())
