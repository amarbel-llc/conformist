fn greeting() -> &'static str {
    "hello"
}

fn main() {
    println!("{}", greeting());
}

#[cfg(test)]
mod tests {
    use super::greeting;

    #[test]
    fn greets() {
        assert_eq!(greeting(), "hello");
    }
}
