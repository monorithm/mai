package presence

// Available tells if this system can show the prompt of a presence check. It shows no
// prompt. "mai doctor" uses it.
func Available() bool { return available() }
