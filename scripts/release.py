import sys
import os
import re
import subprocess

def bump_version(current: str, bump_type: str) -> str:
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

def main():
    if len(sys.argv) < 2 or sys.argv[1] not in ["patch", "minor", "major"]:
        print("Usage: python release.py [patch|minor|major]")
        sys.exit(1)
        
    bump_type = sys.argv[1]
    
    # Locate setup.py (relative to the repo root)
    script_dir = os.path.dirname(os.path.abspath(__file__))
    repo_root = os.path.dirname(script_dir)
    setup_path = os.path.join(repo_root, "python-sdk", "setup.py")
    
    if not os.path.exists(setup_path):
        print(f"Error: setup.py not found at {setup_path}")
        sys.exit(1)
        
    with open(setup_path, "r") as f:
        content = f.read()
        
    # Regex to find version="X.Y.Z" or version='X.Y.Z'
    version_match = re.search(r'version\s*=\s*["\']([^"\']+)["\']', content)
    if not version_match:
        print("Error: Could not find version string in setup.py")
        sys.exit(1)
        
    current_version = version_match.group(1)
    new_version = bump_version(current_version, bump_type)
    print(f"Bumping version from {current_version} to {new_version}...")
    
    # Overwrite the version inside the setup.py content
    new_content = re.sub(
        r'(version\s*=\s*["\'])([^"\']+)(["\'])',
        rf"\g<1>{new_version}\g<3>",
        content
    )
    
    with open(setup_path, "w") as f:
        f.write(new_content)
        
    # Execute git commands via subprocess
    print("Running git add . ...")
    subprocess.run(["git", "add", "."], check=True, cwd=repo_root)
    
    commit_msg = f"chore: release v{new_version}"
    print(f"Running git commit -m '{commit_msg}' ...")
    subprocess.run(["git", "commit", "-m", commit_msg], check=True, cwd=repo_root)
    
    tag_name = f"v{new_version}"
    print(f"Running git tag {tag_name} ...")
    subprocess.run(["git", "tag", tag_name], check=True, cwd=repo_root)
    
    print("\n" + "="*60)
    print(f"Successfully released and tagged {tag_name} locally!")
    print("Please push your changes to trigger the pipeline:")
    print("  git push origin main --tags")
    print("="*60)

if __name__ == "__main__":
    main()
