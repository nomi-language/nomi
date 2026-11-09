// Package goside is the Go half of the Nomi module image: decoding JPEG,
// PNG and WebP, resizing, cropping, and encoding JPEG and PNG, over Go's
// image packages and golang.org/x/image. The Nomi facade (image.nomi) turns
// a Failure into image.Error and holds an *Image as an opaque handle.
//
// An *Image is never changed after it is made: every operation answers a
// new one, as the Nomi values over it require. A failed call answers a
// shared empty one beside its Failure, so no nil handle reaches Nomi.
//
// The decoder is chosen from the data's leading bytes (sniff). Decode reads
// the header first and refuses an image declaring more than its maxPixels,
// since Go's decoders allocate for the declared size.
//
// Resizing is x/image's draw.CatmullRom. x/image has no Lanczos kernel; on a
// 2048 by 1536 photo, Catmull-Rom's 800 by 600 and 267 by 200 downscales are
// within 46 and 43 dB PSNR of disintegration/imaging's Lanczos, below what
// an eye sees, so the module needs no third-party Go beyond x/image.
//
// No call takes a context. Neither Go's decoders nor x/image's scalers can
// stop partway, and the pixel limit Decode enforces bounds every call, so
// a cancelled task's call finishes and the task ends after it. A call runs
// on the calling task's goroutine and does not hold up other tasks.
package goside
