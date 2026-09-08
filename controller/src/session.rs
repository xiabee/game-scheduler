//! Session logging for the dry-run observation loop (NC0): one TSV line
//! per cycle plus a final SUMMARY line. TSV over JSON keeps NC0
//! dependency-free; NC6 will define the real JSON-lines schema when the
//! scheduler integration lands. File write failures degrade to a single
//! warning — observation must never die because logging hiccuped.

use crate::pipeline::CycleReport;
use std::fs::File;
use std::io::Write;
use std::path::Path;
use std::time::Duration;

/// One per-cycle record, rendered at the moment of logging.
pub struct SessionLine<'a> {
    pub cycle: u32,
    pub elapsed_ms: u128,
    pub cycle_duration: Duration,
    pub backend: &'a str,
    pub report: &'a CycleReport,
}

/// Sanitize a field for TSV: newlines/tabs become spaces so a record is
/// always exactly one line.
fn field(s: &str) -> String {
    s.replace(['\t', '\r', '\n'], " ")
}

/// Describe a verdict compactly: `allow`, `skip:<reason>`, ...
fn verdict_tag(v: &crate::safety::GovernorVerdict) -> String {
    use crate::safety::GovernorVerdict::*;
    match v {
        Allow => "allow".into(),
        Skip { reason } => format!("skip:{}", field(reason)),
        Pause { reason } => format!("pause:{}", field(reason)),
        Stop { reason } => format!("stop:{}", field(reason)),
    }
}

/// Append-only session logger. Created lazily on the first write so a bad
/// path cannot prevent the loop from running.
pub struct SessionLogger {
    path: String,
    file: Option<File>,
    warned: bool,
    pub written: u32,
}

impl SessionLogger {
    pub fn new(path: impl Into<String>) -> SessionLogger {
        SessionLogger {
            path: path.into(),
            file: None,
            warned: false,
            written: 0,
        }
    }

    fn file(&mut self) -> Option<&mut File> {
        if self.file.is_none() {
            if let Some(parent) = Path::new(&self.path).parent() {
                let _ = std::fs::create_dir_all(parent);
            }
            self.file = File::options()
                .create(true)
                .append(true)
                .open(&self.path)
                .ok();
        }
        self.file.as_mut()
    }

    fn warn_once(&mut self, e: &std::io::Error) {
        if !self.warned {
            eprintln!(
                "session-log: writes to {:?} failing ({e}); continuing without log",
                self.path
            );
            self.warned = true;
        }
    }

    /// Write one CYCLE line. `summary_verdict` is the action verdict tag or
    /// `none` when the cycle had no planned action.
    pub fn write_cycle(&mut self, line: &SessionLine) {
        let Some(f) = self.file() else {
            return;
        };
        let d = &line.report.client_detections;
        let det_summary: Vec<String> = d
            .iter()
            .zip(line.report.desktop_points.iter())
            .map(|(det, (dx, dy))| {
                let c = det.rect.center();
                format!(
                    "{},{:.1},{:.1},{:.1},{:.1},{:.2},{:.0},{:.0}",
                    field(&det.label),
                    c.0,
                    c.1,
                    det.rect.w,
                    det.rect.h,
                    det.confidence,
                    dx,
                    dy
                )
            })
            .collect();
        let verdict = match &line.report.action_verdict {
            Some(v) => verdict_tag(v),
            None => "none".into(),
        };
        let record = format!(
            "CYCLE\t{}\t{}\t{:?}\t{}\t{}\t{}\t{}\n",
            line.cycle,
            line.elapsed_ms,
            line.cycle_duration,
            field(line.backend),
            verdict_tag(&line.report.pre_verdict),
            verdict,
            det_summary.join(";")
        );
        match f.write_all(record.as_bytes()).and_then(|_| f.flush()) {
            Ok(()) => self.written += 1,
            Err(e) => self.warn_once(&e),
        }
    }

    /// Write the final SUMMARY line (end-state of the session).
    pub fn write_summary(
        &mut self,
        cycles: u32,
        allowed: u32,
        distinct_verdicts: u32,
        outcome: &str,
    ) {
        let Some(f) = self.file() else {
            return;
        };
        let record = format!(
            "SUMMARY\tcycles={cycles}\tallowed={allowed}\tdistinct_verdicts={distinct_verdicts}\toutcome={}\n",
            field(outcome)
        );
        match f.write_all(record.as_bytes()).and_then(|_| f.flush()) {
            Ok(()) => self.written += 1,
            Err(e) => self.warn_once(&e),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pipeline::CycleReport;
    use crate::safety::GovernorVerdict;
    use crate::transform::Rect;
    use crate::vision::Detection;

    fn report_with_detection() -> CycleReport {
        CycleReport {
            cycle: 7,
            pre_verdict: GovernorVerdict::Allow,
            geometry_ok: true,
            client_detections: vec![Detection {
                label: "moving_rect".into(),
                rect: Rect::new(10.0, 20.0, 30.0, 40.0),
                confidence: 0.97,
            }],
            desktop_points: vec![(40.0, 60.0)],
            action_verdict: Some(GovernorVerdict::Allow),
        }
    }

    #[test]
    fn writes_cycle_and_summary_lines_with_tsv_shape() {
        let dir = std::env::temp_dir().join(format!("nf_session_{}", std::process::id()));
        let path = dir.join("session.tsv");
        let _ = std::fs::remove_file(&path);
        {
            let mut log = SessionLogger::new(path.to_string_lossy().to_string());
            let report = report_with_detection();
            let line = SessionLine {
                cycle: 7,
                elapsed_ms: 1234,
                cycle_duration: Duration::from_millis(66),
                backend: "gdi",
                report: &report,
            };
            log.write_cycle(&line);
            log.write_summary(10, 9, 2, "completed");
            assert_eq!(log.written, 2);
        }
        let content = std::fs::read_to_string(&path).expect("read back");
        let lines: Vec<&str> = content.lines().collect();
        assert_eq!(lines.len(), 2);
        assert!(
            lines[0].starts_with("CYCLE\t7\t1234\t"),
            "got {:?}",
            lines[0]
        );
        let cols: Vec<&str> = lines[0].split('\t').collect();
        assert_eq!(cols.len(), 8, "8 columns, got {:?}", cols);
        assert!(
            cols[6].starts_with("allow"),
            "action verdict col: {:?}",
            cols[6]
        );
        assert!(
            cols[7].contains("moving_rect,25.0,40.0,30.0,40.0,0.97,40,60"),
            "detection col: {:?}",
            cols[7]
        );
        assert!(
            lines[1].starts_with(
                "SUMMARY\tcycles=10\tallowed=9\tdistinct_verdicts=2\toutcome=completed"
            ),
            "summary: {:?}",
            lines[1]
        );
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn sanitize_fields_keeps_one_line_per_record() {
        assert_eq!(field("a\tb\nc\rd"), "a b c d");
    }
}
