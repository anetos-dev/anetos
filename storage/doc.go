// SPDX-License-Identifier: Apache-2.0

// Package storage keeps files on disks: a local directory (the
// default), memory, or an S3-compatible bucket (drivers/s3). A disk is
// configured with STORAGE_* settings; STORAGE_DISKS names more disks,
// configured with STORAGE_<NAME>_*.
//
//	st, err := storage.ForApp(app, s3.Driver())
//
//	disk, err := storage.From(ctx)
//	err = disk.PutUpload(ctx, "avatars/"+id+".png", in.Avatar)
//	url, err := disk.TemporaryURL(ctx, "invoices/42.pdf", 15*time.Minute)
//
// Paths are slash-separated and relative ("avatars/42.png"), checked
// rather than cleaned ([CheckPath]), so a path built from user input
// can't leave the disk. Files are served with a disk's Handler (local
// files) or from the bucket; private files through signed temporary
// URLs.
package storage
