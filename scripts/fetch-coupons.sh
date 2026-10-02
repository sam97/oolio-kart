#!/bin/sh
# Fills /data with the three gzipped coupon base files. Each file is copied
# from /seed when it is there, and downloaded otherwise. Files already in
# /data are kept, so this only does work on the first run.
#
# Only .gz files go into /data: the coupons server would count a code found in
# both couponbase1.gz and couponbase1.txt as appearing in two files.
set -eu

base_url=https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com

for number in 1 2 3; do
	name="couponbase$number.gz"
	if [ -s "/data/$name" ]; then
		echo "$name: already present"
		continue
	fi

	# Dot-prefixed, so the coupons server would ignore a leftover partial file.
	partial="/data/.$name.partial"
	if [ -s "/seed/$name" ]; then
		echo "$name: copying from seed folder"
		cp "/seed/$name" "$partial"
	else
		echo "$name: downloading"
		wget -q -O "$partial" "$base_url/$name"
	fi
	gzip -t "$partial" # catches truncated copies and downloads
	mv "$partial" "/data/$name"
done

# The coupons server runs as distroless "nonroot" and saves its results here.
chown -R 65532:65532 /data
echo "coupon data ready"
