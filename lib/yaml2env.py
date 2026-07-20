#!/usr/bin/env python3
"""Flatten cluster.yaml into CFG_* shell variable assignments.

Usage: yaml2env.py <path-to-cluster.yaml>

Prints `KEY=value` lines, one per scalar leaf, suitable for `eval` in bash.
Dict keys are joined with underscores and upper-cased (network.main.cidr ->
CFG_NETWORK_MAIN_CIDR). Lists of scalars are comma-joined into one value;
lists of dicts are index-suffixed (foo.bar.0.name, foo.bar.1.name, ...).

Kept schema-agnostic on purpose: this must not need edits when
cluster.yaml.example grows a field.
"""
import shlex
import sys

try:
    import yaml
except ImportError:
    sys.stderr.write(
        "yaml2env.py: PyYAML not found — install with 'pip3 install pyyaml'\n"
    )
    sys.exit(1)


def sanitize(key: str) -> str:
    out = []
    for ch in str(key):
        out.append(ch.upper() if (ch.isalnum()) else "_")
    return "".join(out)


def emit(prefix, value, lines):
    if isinstance(value, dict):
        for k, v in value.items():
            emit(f"{prefix}_{sanitize(k)}", v, lines)
        return
    if isinstance(value, list):
        if all(not isinstance(v, (dict, list)) for v in value):
            joined = ",".join("" if v is None else str(v) for v in value)
            lines.append(f"{prefix}={shlex.quote(joined)}")
        else:
            for i, v in enumerate(value):
                emit(f"{prefix}_{i}", v, lines)
        return
    if value is None:
        value = ""
    if isinstance(value, bool):
        value = "true" if value else "false"
    lines.append(f"{prefix}={shlex.quote(str(value))}")


def main():
    if len(sys.argv) != 2:
        sys.stderr.write("usage: yaml2env.py <cluster.yaml>\n")
        sys.exit(2)
    path = sys.argv[1]
    with open(path, "r") as f:
        doc = yaml.safe_load(f)
    if not isinstance(doc, dict):
        sys.stderr.write(f"yaml2env.py: {path} did not parse to a mapping\n")
        sys.exit(1)
    lines = []
    for k, v in doc.items():
        emit(f"CFG_{sanitize(k)}", v, lines)
    print("\n".join(lines))


if __name__ == "__main__":
    main()
