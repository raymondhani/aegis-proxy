"""
release.py — Aegis version bump and release tagging script.

Usage:
    python scripts/release.py [--dry-run] [--pre-release LABEL] <patch|minor|major>

Arguments:
    patch | minor | major   Version component to increment.

Options:
    --dry-run               Print all actions without executing any git commands
                            or modifying files. Safe to call from CI pre-checks.
    --pre-release LABEL     Append a pre-release suffix to the version tag.
                            LABEL must match: alpha, beta, or rc.N (e.g. rc.1).
                            Pre-release tags (e.g. v1.2.3-rc.1) will NOT
                            promote 'latest' in the Docker publish workflow.

Examples:
    python scripts/release.py patch
    python scripts/release.py --dry-run minor
    python scripts/release.py --pre-release rc.1 minor
"""

import sys
import os
import re
import subprocess
import argparse

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
SEMVER_PATTERN = re.compile(r'^\d+\.\d+\.\d+$')
PRE_RELEASE_PATTERN = re.compile(r'^(alpha|beta|rc\.\d+)$')

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def bump_version(current: str, bump_type: str) -> str:
    """Increment the specified component of a semver string."""
    parts = list(map(int, current.split(".")))
    if bump_type == "major":
        parts[0] += 1
        parts[1] = 0
        parts[2] = 0
    elif bump_type == "minor":
        parts[1] += 1
        parts[2] = 0
    elif bump_type == "patch":
        parts[2] += 1
    return ".".join(map(str, parts))


def validate_semver(version: str) -> None:
    """Abort if *version* is not a clean X.Y.Z semver string."""
    if not SEMVER_PATTERN.match(version):
        print(f"ERROR: Computed version '{version}' is not valid semver (X.Y.Z).")
        print("       Check setup.py for a malformed version string and try again.")
        sys.exit(1)


def validate_pre_release_label(label: str) -> None:
    """Abort if the pre-release label doesn't match the allowed patterns."""
    if not PRE_RELEASE_PATTERN.match(label):
        print(f"ERROR: Pre-release label '{label}' is invalid.")
        print("       Allowed formats: alpha, beta, rc.1, rc.2, ...")
        sys.exit(1)


def check_dirty_tree(repo_root: str, dry_run: bool) -> None:
    """Warn (or abort in non-dry-run mode) if the working tree has uncommitted changes."""
    result = subprocess.run(
        ["git", "status", "--porcelain"],
        capture_output=True,
        text=True,
        cwd=repo_root,
    )
    if result.stdout.strip():
        lines = result.stdout.strip().splitlines()
        print("WARNING: Working tree has uncommitted changes:")
        for line in lines[:10]:          # cap output to 10 lines
            print(f"  {line}")
        if len(lines) > 10:
            print(f"  ... and {len(lines) - 10} more file(s).")
        if dry_run:
            print("  (dry-run: continuing anyway)\n")
        else:
            print(
                "\nERROR: Refusing to create a release commit on a dirty tree.\n"
                "       Please commit or stash your changes first."
            )
            sys.exit(1)


def run(cmd: list[str], cwd: str, dry_run: bool, description: str) -> None:
    """Run a shell command, or print it if dry_run is True."""
    display = " ".join(cmd)
    if dry_run:
        print(f"  [dry-run] would run: {display}")
    else:
        print(f"  Running: {display}")
        subprocess.run(cmd, check=True, cwd=cwd)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Bump the Aegis version and create a git release tag.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "bump_type",
        choices=["patch", "minor", "major"],
        help="Version component to increment.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print all actions without modifying files or running git commands.",
    )
    parser.add_argument(
        "--pre-release",
        metavar="LABEL",
        default=None,
        help="Append a pre-release suffix (e.g. rc.1, beta, alpha) to the tag.",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    bump_type   = args.bump_type
    dry_run     = args.dry_run
    pre_release = args.pre_release

    if dry_run:
        print("[DRY-RUN] No files or git state will be modified.\n")

    # Validate pre-release label before doing any file I/O
    if pre_release is not None:
        validate_pre_release_label(pre_release)

    # Locate setup.py relative to the repo root
    script_dir = os.path.dirname(os.path.abspath(__file__))
    repo_root  = os.path.dirname(script_dir)
    setup_path = os.path.join(repo_root, "python-sdk", "setup.py")

    if not os.path.exists(setup_path):
        print(f"ERROR: setup.py not found at {setup_path}")
        sys.exit(1)

    with open(setup_path, "r") as f:
        content = f.read()

    # Extract current version from setup.py
    version_match = re.search(r'version\s*=\s*["\']([^"\']+)["\']', content)
    if not version_match:
        print("ERROR: Could not find version string in setup.py")
        sys.exit(1)

    current_version = version_match.group(1)
    # Strip any pre-release suffix from the stored version before bumping
    base_version = current_version.split("-")[0]

    # Compute the bumped stable version
    new_base_version = bump_version(base_version, bump_type)

    # Guard: ensure the output is valid semver
    validate_semver(new_base_version)

    # Build the full version string (may include pre-release suffix)
    if pre_release:
        new_version = f"{new_base_version}-{pre_release}"
        tag_name    = f"v{new_version}"
    else:
        new_version = new_base_version
        tag_name    = f"v{new_version}"

    print(f"Bumping version: {current_version} -> {new_version}")
    if pre_release:
        print(f"  Pre-release tag: {tag_name}")
        print("  WARNING: Pre-release tags do NOT promote 'latest' in the Docker workflow.")
    print()

    # Check for a dirty working tree before touching anything
    check_dirty_tree(repo_root, dry_run)

    # Overwrite the version inside setup.py
    new_content = re.sub(
        r'(version\s*=\s*["\'])([^"\']+)(["\'])',
        rf"\g<1>{new_version}\g<3>",
        content,
    )

    if dry_run:
        print(f"  [dry-run] would write setup.py with version = '{new_version}'")
    else:
        with open(setup_path, "w") as f:
            f.write(new_content)
        print(f"  Updated setup.py -> version = '{new_version}'")

    # Git operations
    run(["git", "add", "."],                           repo_root, dry_run, "stage all changes")
    run(["git", "commit", "-m", f"chore: release {tag_name}"],
                                                        repo_root, dry_run, "create release commit")
    run(["git", "tag", tag_name],                      repo_root, dry_run, "create version tag")

    print()
    print("=" * 60)
    if dry_run:
        print(f"[DRY-RUN] Complete. Would have tagged: {tag_name}")
    else:
        print(f"SUCCESS: Tagged {tag_name} locally.")
    print()
    print("To publish, push your branch and the tag:")
    print("  git push origin main --tags")
    print()
    print("The Docker workflow will:")
    if pre_release:
        print(f"  - Build linux/amd64 + linux/arm64 images tagged '{new_version}'")
        print("  - NOT update 'latest' (pre-release tag)")
    else:
        print(f"  - Build linux/amd64 + linux/arm64 images tagged '{new_version}' and 'latest'")
    print("=" * 60)


if __name__ == "__main__":
    main()
