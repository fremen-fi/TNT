#!/usr/bin/env python3
"""Edit tnt-version.json, the update manifest served from the OVH bucket.

Two independent operations, deliberately kept apart so a release never has to
wait on copy being written:

  bump       Set version + release_date for the platforms that actually built.
             Never invents release notes: a version with none published yet
             gets an empty string, and the app hides the notes block.

  set-notes  Attach release notes to a version after the fact, wherever that
             version appears (current platforms and history alike).

History is derived, not hand-maintained: any version the manifest has ever
mentioned that is no longer current on some platform is a history entry.
That self-heals the gaps left by earlier releases.
"""

import argparse
import json
import sys

PLATFORMS = ("darwin-arm64", "darwin-amd64", "windows", "linux")


def version_key(v):
    """Sort key for '1.4.10' style versions; unparseable ones sort last."""
    parts = []
    for chunk in str(v).split("."):
        digits = "".join(c for c in chunk if c.isdigit())
        parts.append(int(digits) if digits else 0)
    return tuple(parts) or (0,)


def known_notes(manifest):
    """version -> (notes, date) for every version the manifest mentions."""
    seen = {}
    entries = list(manifest.get("platforms", {}).values())
    entries += list(manifest.get("history", []))
    for e in entries:
        v = e.get("version")
        if not v:
            continue
        notes = e.get("release_notes", "") or ""
        date = e.get("release_date", "") or ""
        prev_notes, prev_date = seen.get(v, ("", ""))
        # Prefer whichever copy actually carries text.
        seen[v] = (notes or prev_notes, date or prev_date)
    return seen


def rebuild_history(manifest, notes_by_version):
    """History = every version ever mentioned, minus the ones still shipping."""
    live = {p.get("version") for p in manifest.get("platforms", {}).values()}
    past = [v for v in notes_by_version if v and v not in live]
    past.sort(key=version_key, reverse=True)
    manifest["history"] = [
        {
            "version": v,
            "release_notes": notes_by_version[v][0],
            "release_date": notes_by_version[v][1],
        }
        for v in past
    ]


STRUCTURAL = ("download_url", "supported_platforms")


def apply_template(manifest, template_path):
    """Take repo-owned structure from the template, leave runtime state alone.

    Download URLs and platform labels are config and belong in git; versions,
    dates and notes are state and belong to whatever the bucket already serves.
    Merging only the structural keys is what stops a stale repo copy from
    reverting live versions or re-publishing months-old release notes.
    """
    template = json.load(open(template_path))
    # Only on a cold bucket: seed past releases so bootstrapping doesn't drop them.
    if not manifest.get("history") and not manifest.get("platforms"):
        manifest["history"] = list(template.get("history", []))
    platforms = manifest.setdefault("platforms", {})
    for key, tmpl in template.get("platforms", {}).items():
        entry = platforms.setdefault(key, dict(tmpl))
        for field in STRUCTURAL:
            if field in tmpl:
                entry[field] = tmpl[field]


def cmd_bump(args):
    manifest = json.load(open(args.file))
    if args.template:
        apply_template(manifest, args.template)
    notes_by_version = known_notes(manifest)

    # Notes already published for this version survive a rebuild; a brand-new
    # version starts with none and stays releasable.
    notes, _ = notes_by_version.get(args.version, ("", ""))

    platforms = manifest.setdefault("platforms", {})
    for key in args.platform:
        if key not in platforms:
            sys.exit(f"manifest has no platform {key!r}; known: {sorted(platforms)}")
        platforms[key]["version"] = args.version
        platforms[key]["release_date"] = args.date
        platforms[key]["release_notes"] = notes

    notes_by_version[args.version] = (notes, args.date)
    rebuild_history(manifest, notes_by_version)

    json.dump(manifest, open(args.out, "w"), indent=2, ensure_ascii=False)
    open(args.out, "a").write("\n")


def cmd_set_notes(args):
    manifest = json.load(open(args.file))

    hit = 0
    for entry in manifest.get("platforms", {}).values():
        if entry.get("version") == args.version:
            entry["release_notes"] = args.notes
            hit += 1
    for entry in manifest.get("history", []):
        if entry.get("version") == args.version:
            entry["release_notes"] = args.notes
            hit += 1

    if not hit:
        mentioned = sorted(known_notes(manifest), key=version_key, reverse=True)
        sys.exit(
            f"version {args.version!r} is not in the manifest yet — run this "
            f"after the release build has published it.\n"
            f"versions present: {', '.join(mentioned)}"
        )

    json.dump(manifest, open(args.out, "w"), indent=2, ensure_ascii=False)
    open(args.out, "a").write("\n")
    print(f"set notes on {hit} entr{'y' if hit == 1 else 'ies'} for {args.version}")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest="cmd", required=True)

    b = sub.add_parser("bump", help="set version/date for built platforms")
    b.add_argument("--file", required=True)
    b.add_argument("--out", required=True)
    b.add_argument("--version", required=True)
    b.add_argument("--date", required=True)
    b.add_argument("--platform", action="append", default=[], choices=PLATFORMS)
    b.add_argument("--template", help="repo copy supplying download_url / supported_platforms")
    b.set_defaults(func=cmd_bump)

    n = sub.add_parser("set-notes", help="attach release notes to a version")
    n.add_argument("--file", required=True)
    n.add_argument("--out", required=True)
    n.add_argument("--version", required=True)
    n.add_argument("--notes", required=True)
    n.set_defaults(func=cmd_set_notes)

    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
