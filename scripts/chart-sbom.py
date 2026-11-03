#!/usr/bin/env python3
"""SBOM (CycloneDX 1.5, JSON) Helm-чарта kubron.

syft не знает формат чарта, поэтому состав описывается здесь: сам чарт и контейнерный образ, на который он ссылается
(по digest). Зависимостей-подчартов нет: скрипт падает, если они появились в Chart.yaml, чтобы SBOM не стал неполным
молча. Состав образа (пакеты Go, npm, дистрибутив) — отдельный SBOM образа, который делает syft.

Использование:
  chart-sbom.py --chart deploy/helm/kubron --version 1.2.3 --image ghcr.io/o/kubron \
      --image-digest sha256:… --chart-repo oci://ghcr.io/o/charts --out chart.cdx.json
"""
import argparse
import datetime as dt
import json
import pathlib
import re
import sys
import urllib.parse
import uuid


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--chart", required=True, help="каталог чарта")
    ap.add_argument("--version", required=True)
    ap.add_argument("--image", required=True, help="репозиторий образа без тега, ghcr.io/owner/kubron")
    ap.add_argument("--image-digest", required=True, help="sha256:…")
    ap.add_argument("--chart-repo", required=True, help="oci://ghcr.io/owner/charts")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()

    if not re.fullmatch(r"sha256:[0-9a-f]{64}", a.image_digest):
        print(f"ERROR: неверный digest образа: {a.image_digest}", file=sys.stderr)
        return 1
    chart_yaml = (pathlib.Path(a.chart) / "Chart.yaml").read_text(encoding="utf-8")
    if re.search(r"^dependencies:", chart_yaml, re.M):
        print("ERROR: в Chart.yaml появились dependencies: SBOM чарта нужно расширить", file=sys.stderr)
        return 1

    q = lambda s: urllib.parse.quote(s, safe="")  # noqa: E731
    chart_ref = f"pkg:helm/kubron@{a.version}?repository_url={q(a.chart_repo)}"
    image_name = a.image.rsplit("/", 1)[-1]
    image_ref = f"pkg:oci/{image_name}@{q(a.image_digest)}?repository_url={q(a.image)}&tag={q(a.version)}"
    bom = {
        "bomFormat": "CycloneDX",
        "serialNumber": f"urn:uuid:{uuid.uuid4()}",
        "specVersion": "1.5",
        "version": 1,
        "metadata": {
            "timestamp": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "tools": {"components": [{"type": "application", "name": "scripts/chart-sbom.py"}]},
            "component": {
                "type": "application",
                "bom-ref": chart_ref,
                "name": "kubron",
                "version": a.version,
                "purl": chart_ref,
                "licenses": [{"license": {"id": "Apache-2.0"}}],
            },
        },
        "components": [
            {
                "type": "container",
                "bom-ref": image_ref,
                "name": a.image,
                "version": a.version,
                "purl": image_ref,
                "hashes": [{"alg": "SHA-256", "content": a.image_digest.split(":", 1)[1]}],
                "licenses": [{"license": {"id": "Apache-2.0"}}],
            }
        ],
        "dependencies": [{"ref": chart_ref, "dependsOn": [image_ref]}, {"ref": image_ref, "dependsOn": []}],
    }
    pathlib.Path(a.out).parent.mkdir(parents=True, exist_ok=True)
    pathlib.Path(a.out).write_text(json.dumps(bom, indent=2) + "\n", encoding="utf-8")
    print(f"OK: {a.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
