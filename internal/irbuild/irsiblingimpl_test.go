package irbuild

import "testing"

// The Tour's opaque-type program (modules-and-imports.md:L216): main calls
// impl functions a sibling file declares for its opaque type.
func TestIRSiblingImpl_TourOpaqueTypes(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  ids.UserId
end

fn main()
  case UserId.new(42)
    Ok(id) -> io.print(UserId.value(id))
    Err(e) -> io.print("error: ${e}")
  end
  case UserId.new(-1)
    Ok(_) -> io.print("unexpected")
    Err(e) -> io.print("error: ${e}")
  end

  // Uncomment to see the opaque barrier — main.nomi can hold a
  // UserId, but can't construct or destructure one directly:
  // bad = UserId(7)
end

`, "42\nerror: user IDs must be positive\n", map[string]string{
		"ids.nomi": `pub opaque type UserId Int

impl UserId
  pub fn new(n: Int) -> Result<UserId, String>
    if n > 0 then
      Ok(UserId(n))
    else
      Err("user IDs must be positive")
    end
  end

  pub fn value(id: UserId) -> Int
    UserId(n) = id // only ids.nomi can destructure UserId
    n
  end
end

`,
		"nomi.toml": `[module]

name = "ids_demo"

entry_points = ["main"]
`,
	})
}
