#!/bin/bash
set -e

WORKSPACE_DIR=$(pwd)
BUILD_DIR="${WORKSPACE_DIR}/build_deps"

echo "Downloading and building Piper dependencies..."

# Create a clean build directory
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

# Download Piper source via wget
echo "Downloading Piper source archive..."
wget -qO piper-source.tar.gz https://github.com/rhasspy/piper/archive/refs/heads/master.tar.gz
tar -xzf piper-source.tar.gz
mv piper-master piper
cd piper

# CMake's FetchContent isolates environments and completely ignores global ~/.gitconfig aliases!
# We will create a robust `git` wrapper that intercepts CMake's isolated commands directly
# and forcefully changes the HTTPS/Git protocols to your working SSH connection!
mkdir -p "$BUILD_DIR/fake_bin"
cat << 'EOF' > "$BUILD_DIR/fake_bin/git"
#!/bin/bash
declare -a args
for arg in "$@"; do
    arg="${arg/https:\/\/github.com\//git@github.com:}"
    arg="${arg/git:\/\/github.com\//git@github.com:}"
    args+=("$arg")
done
exec /usr/bin/git "${args[@]}"
EOF
chmod +x "$BUILD_DIR/fake_bin/git"
export PATH="$BUILD_DIR/fake_bin:$PATH"

# Run make to fetch dependencies and build libpiper.a
echo "Building Piper statically. This might take a few minutes as it downloads ONNX Runtime and espeak-ng..."
export PIE=0

max_retries=5
count=0
until make; do
    exit_code=$?
    count=$((count + 1))
    if [ $count -ge $max_retries ]; then
        echo "Make failed after $max_retries attempts. Network might be permanently blocking Git."
        exit $exit_code
    fi
    echo "Make failed. Retrying... ($count/$max_retries)"
    sleep 2
done

# Copy artifacts out
cd "$WORKSPACE_DIR"
mkdir -p include/piper
mkdir -p lib

# Copy Piper headers
cp -r "$BUILD_DIR"/piper/src/cpp/* include/piper/

# Copy built static library and dependencies
cp "$BUILD_DIR"/piper/build/libpiper.a lib/
cp -r "$BUILD_DIR"/piper/lib/* lib/ || true

echo "Dependencies built and copied to include/ and lib/."
echo "You may delete the build_deps/ directory manually if desired."
