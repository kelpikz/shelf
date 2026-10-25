//! Device flow: poll until the Owner approves or denies. RFC 8628, section 3.5.

use std::time::Duration;

use anyhow::{Result, bail};

use crate::client::{NewKey, Poll};

const SLOW_DOWN_STEP: Duration = Duration::from_secs(5);
const MIN_INTERVAL: Duration = Duration::from_secs(1);

pub fn wait_for_key(
    interval: Duration,
    mut poll: impl FnMut() -> Result<Poll>,
    mut sleep: impl FnMut(Duration),
) -> Result<NewKey> {
    let mut interval = interval.max(MIN_INTERVAL);
    loop {
        sleep(interval);
        match poll()? {
            Poll::Wait => {}
            Poll::SlowDown => interval += SLOW_DOWN_STEP,
            Poll::Approved(key) => return Ok(key),
            Poll::Denied => bail!("The login was denied."),
            Poll::Expired => bail!("The login code expired. Run: shelf login"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn key() -> NewKey {
        NewKey {
            key: "shelf_abc".into(),
            expires_at: 1_800_000_000,
        }
    }

    /// Runs wait_for_key with answers from the list. Gives the result and the sleeps.
    fn run(answers: Vec<Poll>) -> (Result<NewKey>, Vec<u64>) {
        let mut answers = answers.into_iter();
        let mut sleeps = Vec::new();
        let got = wait_for_key(
            Duration::from_secs(5),
            || Ok(answers.next().expect("polled after the last answer")),
            |d| sleeps.push(d.as_secs()),
        );
        (got, sleeps)
    }

    #[test]
    fn waits_until_approved() {
        let (got, sleeps) = run(vec![Poll::Wait, Poll::Wait, Poll::Approved(key())]);
        assert_eq!(got.unwrap().key, "shelf_abc");
        assert_eq!(sleeps, [5, 5, 5]);
    }

    #[test]
    fn slow_down_adds_five_seconds() {
        let (got, sleeps) = run(vec![
            Poll::SlowDown,
            Poll::Wait,
            Poll::SlowDown,
            Poll::Approved(key()),
        ]);
        assert!(got.is_ok());
        assert_eq!(sleeps, [5, 10, 10, 15]);
    }

    #[test]
    fn denied_and_expired_stop_with_an_error() {
        let (got, _) = run(vec![Poll::Wait, Poll::Denied]);
        assert!(got.err().unwrap().to_string().contains("denied"));
        let (got, _) = run(vec![Poll::Expired]);
        assert!(got.err().unwrap().to_string().contains("expired"));
    }

    #[test]
    fn zero_interval_still_waits() {
        let mut sleeps = Vec::new();
        let mut answers = vec![Poll::Wait, Poll::Approved(key())].into_iter();
        wait_for_key(
            Duration::ZERO,
            || Ok(answers.next().unwrap()),
            |d| sleeps.push(d),
        )
        .unwrap();
        assert!(sleeps.iter().all(|d| *d >= Duration::from_secs(1)));
    }
}
