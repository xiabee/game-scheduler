/// NC9 feeds ffmpeg-style extractions: RGB PNGs (no alpha) must decode
/// correctly instead of panicking on the RGBA-stride assumption.
#[test]
fn rgb_png_decodes_without_alpha() {
    use png::BitDepth;
    use png::ColorType::Rgb;

    let (w, h) = (5usize, 3usize);
    // RGB pixel data: a red-ish gradient with a distinctive marker pixel
    let mut raw = vec![0u8; w * h * 3];
    for y in 0..h {
        for x in 0..w {
            let i = (y * w + x) * 3;
            raw[i] = 200;
            raw[i + 1] = 40;
            raw[i + 2] = 16;
            if (x, y) == (3, 1) {
                raw[i] = 1;
                raw[i + 1] = 2;
                raw[i + 2] = 3;
            }
        }
    }
    let mut out = Vec::new();
    {
        let mut encoder = png::Encoder::new(&mut out, w as u32, h as u32);
        encoder.set_color(Rgb);
        encoder.set_depth(BitDepth::Eight);
        let mut writer = encoder.write_header().expect("header");
        writer.write_image_data(&raw).expect("data");
    }

    let frame = controller::replay::decode_png_bytes(&out).expect("decode RGB png");
    assert_eq!((frame.width, frame.height), (w as u32, h as u32));
    // decoder swaps to BGRA: marker pixel reads (3,2,1,255)
    assert_eq!(frame.pixel(3, 1), Some([3, 2, 1, 255]));
    // a regular pixel: RGB(200,40,16) -> BGRA (16,40,200,255)
    assert_eq!(frame.pixel(0, 0), Some([16, 40, 200, 255]));
}
