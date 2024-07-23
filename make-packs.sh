#!/bin/bash

# Create distribution directory
rm -rf "$(dirname $(dirname $(realpath $0)))/dist-packs"
mkdir "$(dirname $(dirname $(realpath $0)))/dist-packs"
VERSION="go1.23.42"

# Write spoofed version to VERSION file
echo $VERSION > ./VERSION

pack() {
    rm -rf pkg
    rm -rf bin

    local goos=$1
    local goarch=$2
    cd src
    echo "Packing $1"
    GOOS=$goos GOARCH=$goarch bash make.bash -distpack
    cd ..

    local hash=$(md5sum "pkg/distpack/$VERSION.$goos-$goarch.tar.gz" | cut -c1-12)

    cp "pkg/distpack/$VERSION.$goos-$goarch.tar.gz" "$(dirname $(dirname $(realpath $0)))/dist-packs/$VERSION.$goos-$goarch.$hash.tar.gz"
}

pack linux amd64
pack darwin amd64
pack linux arm64
pack darwin arm64

# Re-add FIXME note to VERSION file
echo "$VERSION
// FIXME: Remove this" > ./VERSION
