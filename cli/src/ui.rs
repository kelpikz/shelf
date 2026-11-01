//! Terminal output. anstream drops the colors when the output is not a
//! terminal, or when NO_COLOR is set.

use std::fmt::Display;

use anstream::{eprintln, println};
use anstyle::{AnsiColor, Color, Style};

const fn fg(color: AnsiColor) -> Style {
    Style::new().fg_color(Some(Color::Ansi(color)))
}

pub const GOOD: Style = fg(AnsiColor::Green).bold();
const BAD: Style = fg(AnsiColor::Red).bold();
pub const WARN: Style = fg(AnsiColor::Yellow).bold();
const DIM: Style = fg(AnsiColor::BrightBlack);
const CMD: Style = fg(AnsiColor::Cyan);
pub const LINK: Style = fg(AnsiColor::Cyan).underline();
pub const CODE: Style = fg(AnsiColor::Yellow).bold();
pub const BOLD: Style = Style::new().bold();

/// clap help: green headers, cyan commands and flags.
pub const HELP: clap::builder::Styles = clap::builder::Styles::styled()
    .header(fg(AnsiColor::Green).bold().underline())
    .usage(fg(AnsiColor::Green).bold().underline())
    .literal(fg(AnsiColor::Cyan).bold())
    .placeholder(CMD);

pub fn ok(msg: impl Display) {
    println!("{GOOD}✓{GOOD:#} {msg}");
}

pub fn bad(msg: impl Display) {
    println!("{BAD}✗{BAD:#} {msg}");
}

pub fn warn(msg: impl Display) {
    println!("{WARN}!{WARN:#} {msg}");
}

pub fn detail(msg: impl Display) {
    println!("  {DIM}{msg}{DIM:#}");
}

pub fn dim(msg: impl Display) {
    println!("{DIM}{msg}{DIM:#}");
}

/// "  Run: shelf login"
pub fn hint(command: &str) {
    println!("  {DIM}Run:{DIM:#} {CMD}{command}{CMD:#}");
}

/// The top message, then each cause on its own line.
pub fn error(err: &anyhow::Error) {
    eprintln!("{BAD}error:{BAD:#} {err}");
    for cause in err.chain().skip(1) {
        eprintln!("  {DIM}{cause}{DIM:#}");
    }
}
