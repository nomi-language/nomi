package irbuild

import "testing"

func TestIRUserOperator_Tour(t *testing.T) {
	verifyLambdaProgram(t, `type Score Int

fn score_number(score: Score) -> Int
  Score(n) = score
  n
end

type Day Int

fn day_number(day: Day) -> Int
  Day(n) = day
  n
end

type Days Int

fn days_number(days: Days) -> Int
  Days(n) = days
  n
end

impl Add<Score, Score> for Score
  fn add(lhs: Score, rhs: Score) -> Score
    Score(score_number(lhs) + score_number(rhs))
  end
end

impl Add<Days, Day> for Day
  fn add(lhs: Day, rhs: Days) -> Day
    Day(day_number(lhs) + days_number(rhs))
  end
end

fn main()
  score = Score(2) + Score(3)
  day = Day(10) + Days(4)

  dbg score_number(score)
  dbg day_number(day)
end
`, "dbg line 38: score_number(score) = 5\ndbg line 39: day_number(day) = 14\n")
}

func TestIRUserOperator_OverloadsEffectsAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
type Score Int
type Points Int
fn number(x: Score) -> Int
    Score(n) = x
    n
end
fn points(x: Points) -> Int
    Points(n) = x
    n
end
impl Add<Score, Score> for Score
 fn add(lhs: Score, rhs: Score) -> Score
     Score(number(lhs) + number(rhs))
 end
end
impl Add<Points, Score> for Score
 fn add(lhs: Score, rhs: Points) -> Score
     Score(number(lhs) + points(rhs) * 10)
 end
end
impl Subtract<Score, Score> for Score
 fn subtract(lhs: Score, rhs: Score) -> Score
     Score(number(lhs) - number(rhs))
 end
end
impl Multiply<Score, Score> for Score
 fn multiply(lhs: Score, rhs: Score) -> Score
     Score(number(lhs) * number(rhs))
 end
end
impl Divide<Score, Score> for Score
 fn divide(lhs: Score, rhs: Score) -> Score
     Score(number(lhs) / number(rhs))
 end
end
fn left() -> Score
    io.print("left")
    Score(8)
end
fn right() -> Score
    io.print("right")
    Score(2)
end
fn main()
 io.print(number(left() + right()))
 io.print(number(Score(8) - Score(2)))
 io.print(number(Score(8) * Score(2)))
 io.print(number(Score(8) / Score(2)))
 base = Score(3)
 apply = fn extra: Points -> base + extra
 io.print(number(apply(Points(2))))
end
`, "left\nright\n10\n6\n16\n4\n23\n")
}

func TestIRUserOperator_GenericCaller(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
type Score Int
fn number(x: Score) -> Int
    Score(n) = x
    n
end
impl Add<Score, Score> for Score
 fn add(lhs: Score, rhs: Score) -> Score
     Score(number(lhs) + number(rhs))
 end
end
fn plus<L, R, Out>(lhs: L, rhs: R) -> Out where L: Add<R, Out>
 lhs + rhs
end
fn main()
 sum: Score = plus<Score, Score, Score>(Score(4), Score(5))
 io.print(number(sum))
end
`, "9\n")
}
