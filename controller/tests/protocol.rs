//! Protocol conformance against the REAL binary (NC6): spawn
//! `controller --dry-run --protocol` and assert the stdout stream is a
//! well-formed NC6 session (HELLO first, READY, RESULT last, every line
//! schema-valid). Uses the synthetic backend + probe window: no game, no
//! input, no model.

#![cfg(windows)]

use std::process::{Command, Stdio};

#[test]
fn protocol_session_stream_conforms() {
    if !controller::window::interactive_desktop_available() {
        eprintln!("skipped: no interactive desktop (service context)");
        return;
    }
    let exe = env!("CARGO_BIN_EXE_controller");
    let mut child = Command::new(exe)
        .args([
            "--dry-run",
            "--protocol",
            "--window",
            "@probe",
            "--backend",
            "synthetic",
            "--duration",
            "1",
        ])
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn controller");
    let stdout = child.stdout.take().expect("stdout");
    let status = child.wait().expect("wait");
    assert!(status.success(), "controller exited {status:?}");

    let mut reader = std::io::BufReader::new(stdout);
    let mut lines = Vec::new();
    loop {
        let mut line = String::new();
        match std::io::BufRead::read_line(&mut reader, &mut line) {
            Ok(0) => break,
            Ok(_) => {
                if line.trim().is_empty() {
                    continue;
                }
                lines.push(line);
            }
            Err(e) => panic!("read stdout: {e}"),
        }
    }
    assert!(
        lines.len() >= 3,
        "expected HELLO+READY+RESULT, got {lines:?}"
    );

    let mut saw_ready = false;
    for (i, line) in lines.iter().enumerate() {
        let msg = controller::protocol::Envelope::parse(line)
            .unwrap_or_else(|e| panic!("line {i} violates the schema: {e} — {line}"));
        if i == 0 {
            assert!(
                matches!(msg.payload, controller::protocol::Payload::Hello(_)),
                "first line must be HELLO: {line}"
            );
        }
        if matches!(msg.payload, controller::protocol::Payload::Ready(_)) {
            saw_ready = true;
        }
    }
    assert!(saw_ready, "READY missing");
    let last = lines.last().expect("non-empty");
    let msg = controller::protocol::Envelope::parse(last).expect("last line");
    match msg.payload {
        controller::protocol::Payload::Result(r) => {
            assert_eq!(r.outcome, controller::protocol::Outcome::Done);
        }
        other => panic!("last line must be RESULT, got {other:?}"),
    }
}
