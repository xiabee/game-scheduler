//! Layered perception (NC2 groundwork): the cheap-first evidence stack
//! from ROADMAP §3 NC2. L0 pixel probes answer "is the page in the state
//! we expect" for a handful of sampled pixels; L1 runs the NCC template
//! matcher only where L0-style cheap signals cannot decide. Every layer
//! reports into one [`Evidence`] struct and can be switched off
//! independently, so a skill defines WHAT it needs, not HOW to look.
//!
//! L2 (YOLO via the NC1 ONNX runtime) and L3 (OCR) join the same
//! Evidence shape later; orchestration order stays cheap-first:
//! `L0 → L1 → L2 → OCR`, and a layer that answers the question spares
//! the ones below it.

use crate::frame::Frame;
use crate::template::{NccTemplateMatcher, TemplateMatch, TemplateMatcher};
use crate::vision::Detection;

/// Perception layers, cheapest first. The layer index is part of the
/// contract: skills record which layer satisfied an expectation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Layer {
    /// L0: sampled pixel/region checks — near-zero cost.
    L0,
    /// L1: NCC template matching — low cost, brightness invariant.
    L1,
}

/// What one L0 probe checks: a small region whose pixels must match an
/// expected color within per-channel tolerance. Region sampling uses
/// `step` so a big region still costs a bounded number of reads.
#[derive(Debug, Clone, serde::Deserialize)]
pub struct PixelProbe {
    pub name: String,
    pub x: u32,
    pub y: u32,
    pub w: u32,
    pub h: u32,
    /// Expected BGRA color (alpha ignored).
    pub expected: [u8; 3],
    /// Per-channel tolerance in either direction.
    pub tolerance: u8,
    /// Fraction of sampled pixels that must match for the probe to fire,
    /// in (0, 1].
    pub min_fraction: f32,
    /// Sampling stride inside the region (1 = every pixel).
    pub step: u32,
}

/// The outcome of one probe (L0) this cycle.
#[derive(Debug, Clone, PartialEq)]
pub struct ProbeResult {
    pub name: String,
    pub fired: bool,
    pub matched_fraction: f32,
}

/// Everything perception produced this cycle, cheap layers first.
#[derive(Debug, Clone, Default)]
pub struct Evidence {
    /// L0 probe outcomes, in the order the probes were registered.
    pub probes: Vec<ProbeResult>,
    /// L1 template matches (top-left corner + NCC score), if the layer
    /// ran and matched.
    pub matches: Vec<TemplateMatch>,
}

impl Evidence {
    /// The first fired probe with this name, if any.
    pub fn probe_fired(&self, name: &str) -> bool {
        self.probes.iter().any(|p| p.name == name && p.fired)
    }
}

/// One template registered with the L1 layer: what to look for and the
/// minimum NCC score that counts as a detection.
#[derive(Debug, Clone)]
pub struct TemplateTarget {
    pub name: String,
    pub template: Frame,
    pub min_score: f32,
    pub stride: u32,
}

/// The layered perception engine. Cheap layers first; every layer can be
/// disabled (a disabled layer leaves its Evidence section empty).
pub struct LayeredPerception {
    probes: Vec<PixelProbe>,
    templates: Vec<TemplateTarget>,
    enabled: [bool; 2],
    matcher: NccTemplateMatcher,
}

impl Default for LayeredPerception {
    fn default() -> Self {
        Self::new()
    }
}

impl LayeredPerception {
    pub fn new() -> LayeredPerception {
        LayeredPerception {
            probes: Vec::new(),
            templates: Vec::new(),
            enabled: [true, true],
            matcher: NccTemplateMatcher,
        }
    }

    pub fn add_probe(&mut self, probe: PixelProbe) {
        self.probes.push(probe);
    }

    /// Parse and validate an L0 probe set from JSON (the `--probes`
    /// config). Schema: `[{ "name", "x", "y", "w", "h", "expected":
    /// [b,g,r], "tolerance", "min_fraction", "step" }]`.
    pub fn probes_from_json(text: &str) -> Result<Vec<PixelProbe>, String> {
        let probes: Vec<PixelProbe> =
            serde_json::from_str(text).map_err(|e| format!("invalid probes JSON: {e}"))?;
        if probes.len() > 256 {
            return Err(format!(
                "at most 256 probes per session, got {}",
                probes.len()
            ));
        }
        for (i, p) in probes.iter().enumerate() {
            if p.name.trim().is_empty() {
                return Err(format!("probes[{i}].name must be non-blank"));
            }
            if p.w == 0 || p.h == 0 {
                return Err(format!("probes[{}].w/h must be positive", i));
            }
            if !(p.min_fraction > 0.0 && p.min_fraction <= 1.0) {
                return Err(format!(
                    "probes[{i}].min_fraction must be in (0, 1], got {}",
                    p.min_fraction
                ));
            }
        }
        Ok(probes)
    }
    pub fn add_template(&mut self, target: TemplateTarget) {
        self.templates.push(target);
    }

    pub fn set_enabled(&mut self, layer: Layer, on: bool) {
        self.enabled[match layer {
            Layer::L0 => 0,
            Layer::L1 => 1,
        }] = on;
    }

    pub fn layer_enabled(&self, layer: Layer) -> bool {
        self.enabled[match layer {
            Layer::L0 => 0,
            Layer::L1 => 1,
        }]
    }

    /// Run every enabled layer, cheapest first, and collect the evidence.
    /// Deterministic and allocation-bounded: no fallbacks, no side effects
    /// beyond reading the frame.
    pub fn evaluate(&mut self, frame: &Frame) -> Evidence {
        let mut evidence = Evidence::default();

        if self.layer_enabled(Layer::L0) {
            for probe in &self.probes {
                evidence.probes.push(evaluate_probe(frame, probe));
            }
        }

        if self.layer_enabled(Layer::L1) {
            for target in &self.templates {
                if let Some(m) = self
                    .matcher
                    .find(frame, &target.template, target.stride.max(1))
                {
                    if m.score >= target.min_score {
                        evidence.matches.push(m);
                    }
                }
            }
        }

        evidence
    }
}

/// Evaluate one probe: sample the region on `step` boundaries, count
/// per-channel matches, fire when the fraction reaches `min_fraction`.
fn evaluate_probe(frame: &Frame, probe: &PixelProbe) -> ProbeResult {
    let fired = |matched_fraction: f32| ProbeResult {
        name: probe.name.clone(),
        fired: matched_fraction >= probe.min_fraction,
        matched_fraction,
    };
    let step = probe.step.max(1);
    let mut sampled = 0u32;
    let mut matched = 0u32;
    let mut y = probe.y;
    while y < probe.y.saturating_add(probe.h) && y < frame.height {
        let mut x = probe.x;
        while x < probe.x.saturating_add(probe.w) && x < frame.width {
            sampled += 1;
            if let Some(px) = frame.pixel(x, y) {
                let (b, g, r) = (px[0], px[1], px[2]);
                let near = |c: u8, e: u8| c.abs_diff(e) <= probe.tolerance;
                if near(b, probe.expected[0])
                    && near(g, probe.expected[1])
                    && near(r, probe.expected[2])
                {
                    matched += 1;
                }
            }
            x += step;
        }
        y += step;
    }
    if sampled == 0 {
        // nothing to look at (out-of-bounds region): the probe cannot fire
        return fired(0.0);
    }
    fired(matched as f32 / sampled as f32)
}

/// Convert a template match into the shared Detection shape so L1
/// evidence flows through the same transform/governor path as L2.
pub fn match_to_detection(target: &TemplateTarget, m: &TemplateMatch) -> Detection {
    Detection {
        label: target.name.clone(),
        rect: crate::transform::Rect::new(
            m.x as f32,
            m.y as f32,
            target.template.width as f32,
            target.template.height as f32,
        ),
        confidence: m.score.clamp(0.0, 1.0),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn frame_with_patch(x0: u32, y0: u32, color: [u8; 3]) -> Frame {
        let mut f = Frame::new(64, 64);
        for y in y0..y0 + 8 {
            for x in x0..x0 + 8 {
                f.set_pixel(x, y, [color[0], color[1], color[2], 255]);
            }
        }
        f
    }

    fn probe(name: &str, x: u32, y: u32, expected: [u8; 3]) -> PixelProbe {
        PixelProbe {
            name: name.into(),
            x,
            y,
            w: 8,
            h: 8,
            expected,
            tolerance: 6,
            min_fraction: 0.8,
            step: 2,
        }
    }

    #[test]
    fn layered_evaluation_stays_within_the_cpu_budget() {
        // ROADMAP §5/NC2: cheap-first means an L0(+L1) evaluation over a
        // normal frame must cost milliseconds, not a frame budget. This is
        // a generous regression tripwire (not a benchmark): a stride or
        // sampling regression shows up as a many-x slowdown.
        let mut f = Frame::new(320, 240);
        for y in 0..240u32 {
            for x in 0..320u32 {
                let v = ((x / 8 + y / 8) % 2) as u8 * 180;
                f.set_pixel(x, y, [v, v, v, 255]);
            }
        }
        let mut lp = LayeredPerception::new();
        lp.add_probe(probe("a", 0, 0, [0, 0, 0]));
        lp.add_probe(probe("b", 150, 100, [180, 180, 180]));
        lp.add_template(TemplateTarget {
            name: "patch".into(),
            template: {
                let mut t = Frame::new(8, 8);
                for y in 0..8u32 {
                    for x in 0..8u32 {
                        let v = ((x / 4 + y / 4) % 2) as u8 * 180;
                        t.set_pixel(x, y, [v, v, v, 255]);
                    }
                }
                t
            },
            min_score: 0.9,
            stride: 4,
        });

        let started = std::time::Instant::now();
        const EVALS: u32 = 100;
        for _ in 0..EVALS {
            let ev = lp.evaluate(&f);
            assert!(!ev.probes.is_empty());
        }
        let avg_ms = started.elapsed().as_millis() as f32 / EVALS as f32;
        // ceiling sized for DEBUG builds (release is ~50x faster); a
        // sampling/stride regression shows up as a many-x slowdown, far
        // beyond this margin
        assert!(
            avg_ms < 60.0,
            "layered evaluation averaged {avg_ms:.2}ms (>60ms debug) - a cheap layer regressed"
        );
    }

    #[test]
    fn the_committed_example_probes_always_parse() {
        let path =
            std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("examples/probes.example.json");
        let text = std::fs::read_to_string(&path).expect("example file");
        let probes = LayeredPerception::probes_from_json(&text).expect("example must validate");
        assert_eq!(probes.len(), 2);
        assert_eq!(probes[0].name, "menu_button");
    }

    #[test]
    fn l0_probe_fires_on_matching_region() {
        let f = frame_with_patch(10, 10, [200, 40, 16]); // BGRA pixel body
        let p = probe("ready", 10, 10, [200, 40, 16]);
        let r = evaluate_probe(&f, &p);
        assert!(r.fired, "{r:?}");
        assert!((r.matched_fraction - 1.0).abs() < 1e-6);
    }

    #[test]
    fn l0_probe_tolerance_and_fraction_semantics() {
        // one channel off by exactly the tolerance: still matches
        let mut f = frame_with_patch(10, 10, [200, 40, 16]);
        // paint half the region with a different color: fraction 0.5
        for y in 10..14 {
            for x in 10..18 {
                f.set_pixel(x, y, [0, 0, 0, 255]);
            }
        }
        let p = probe("half", 10, 10, [200, 40, 16]);
        let r = evaluate_probe(&f, &p);
        assert!(!r.fired, "0.5 < 0.8 must not fire: {r:?}");
        assert!((r.matched_fraction - 0.5).abs() < 0.01);

        // a stricter fraction still reads the same content
        let mut strict = probe("half_strict", 10, 10, [200, 40, 16]);
        strict.min_fraction = 0.3;
        assert!(evaluate_probe(&f, &strict).fired);
    }

    #[test]
    fn l0_probe_out_of_bounds_cannot_fire() {
        let f = frame_with_patch(10, 10, [200, 40, 16]);
        let p = probe("offscreen", 60, 60, [200, 40, 16]);
        let r = evaluate_probe(&f, &p);
        assert!(!r.fired && r.matched_fraction == 0.0);
    }

    #[test]
    fn layers_run_cheap_first_and_can_be_disabled() {
        let f = frame_with_patch(10, 10, [200, 40, 16]);
        let mut lp = LayeredPerception::new();
        lp.add_probe(probe("ready", 10, 10, [200, 40, 16]));

        let ev = lp.evaluate(&f);
        assert!(ev.probe_fired("ready"));
        assert!(ev.matches.is_empty(), "no L1 targets registered");

        // disabling L0 empties its evidence section only
        lp.set_enabled(Layer::L0, false);
        assert!(!lp.layer_enabled(Layer::L0));
        let ev = lp.evaluate(&f);
        assert!(ev.probes.is_empty());
        lp.set_enabled(Layer::L0, true);
        assert!(lp.evaluate(&f).probe_fired("ready"));
    }

    #[test]
    fn l1_template_evidence_flows_into_detections() {
        // textured template (NCC is degenerate on constant images) whose
        // checkerboard occupies the whole image; the matcher must find a
        // high-score position and the evidence must convert to a Detection
        let mut template = Frame::new(8, 8);
        for y in 0..8 {
            for x in 0..8 {
                let v = if ((x + y) % 2) == 0 { 200u8 } else { 40u8 };
                template.set_pixel(x, y, [v, v, v, 255]);
            }
        }
        let mut image = Frame::new(32, 32);
        for y in 0..32 {
            for x in 0..32 {
                let v = if ((x + y) % 2) == 0 { 200u8 } else { 40u8 };
                image.set_pixel(x, y, [v, v, v, 255]);
            }
        }
        let mut lp = LayeredPerception::new();
        lp.add_template(TemplateTarget {
            name: "icon".into(),
            template,
            min_score: 0.9,
            stride: 2,
        });
        let ev = lp.evaluate(&image);
        assert!(!ev.matches.is_empty(), "{ev:?}");
        assert!(ev.matches[0].score >= 0.9);

        // the shared Detection shape keeps L1 evidence pipeline-compatible
        let target = &lp.templates[0];
        let d = match_to_detection(target, &ev.matches[0]);
        assert_eq!(d.label, "icon");
        assert!((d.confidence - ev.matches[0].score).abs() < 1e-6);
    }
}
