package irbuild

import "testing"

// ascriptionDecls are the declarations the ascription tests share.
const ascriptionDecls = `import std::io

interface Named
    fn name(value: self) -> String
end

interface Tag
    fn tag(value: self) -> String
end

struct Post
    title: String
end

struct Page
    slug: String
end

impl Named for Post
    fn name(p: Post) -> String
        "post ${p.title}"
    end
end

impl Named for Page
    fn name(p: Page) -> String
        "page ${p.slug}"
    end
end

impl Named for Int
    fn name(n: Int) -> String
        "int ${n}"
    end
end

impl Tag for Int
    fn tag(n: Int) -> String
        "tag ${n}"
    end
end

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

impl Display for Circle
    fn to_string(c: Circle) -> String
        "circle ${c.r}"
    end
end

impl Display for Shape
    fn to_string(s: Shape) -> String
        case s
            .Circle{r:} -> "shape circle ${r}"
            .Dot        -> "dot"
        end
    end
end

fn circle_shape(r: Int) -> Shape
    Circle{r:}
end

fn show(n: Named) -> String
    Named.name(n)
end

fn both(x: Named and Tag) -> String
    "${Named.name(x)} ${Tag.tag(x)}"
end

`

// `value as Type` is the value `x: Type = value` binds (spec §5 *Type
// ascription*): a value ascribed to an interface is an interface value,
// boxed where its runtime type names no impl. Two empty lists of different
// element types held as Display are unequal, ascribed as annotated.
func TestIRAscriptionBoxesAsTheAnnotationDoes_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, ascriptionDecls+`fn main()
    ints: List<Int> = []
    strings: List<String> = []
    a: Display = ints
    b: Display = strings
    a2 = ints as Display
    b2 = strings as Display
    io.inspect([a == b, a2 == b2])
end
`, "[False, False]\n")
}

// An `embeds` enum's value ascribed to Display compares as the enum, not as
// the struct it embeds, as an annotated binding's does.
func TestIRAscriptionOfAnEmbedsEnumIsTheEnum_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, ascriptionDecls+`fn main()
    shape: Display = circle_shape(1)
    circle: Display = Circle{r: 1}
    shape2 = circle_shape(1) as Display
    circle2 = Circle{r: 1} as Display
    io.inspect([shape == circle, shape2 == circle2])
    io.print("${shape2} ${circle2}")
end
`, "[False, False]\nshape circle 1 circle 1\n")
}

// The ascribed form and the annotated form agree where a value of an
// interface is read: a list of two types iterated with an interface call,
// Debug.inspect, an argument, a conjunction, and `as` inside a capture.
func TestIRAscriptionAgreesWithTheAnnotation_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, ascriptionDecls+`fn main()
    annotated: List<Named> = [Post{title: "x"}, Page{slug: "y"}]
    ascribed = [Post{title: "x"} as Named, Page{slug: "y"} as Named]
    io.inspect(annotated |> Iter.map(&Named.name(&1)) |> Iter.to_list())
    io.inspect(ascribed |> Iter.map(&Named.name(&1)) |> Iter.to_list())
    io.print(Debug.inspect(ascribed))

    p: Named = Post{title: "a"}
    p2 = Post{title: "a"} as Named
    io.print("${show(p)} ${show(p2)} ${Debug.inspect(p)} ${Debug.inspect(p2)}")

    c: Named and Tag = 4
    c2 = 4 as (Named and Tag)
    io.print("${both(c)} ${both(c2)}")
    io.inspect(c == c2)

    tags = [1, 2] |> Iter.map(&(&1 as Named)) |> Iter.to_list()
    io.inspect(tags |> Iter.map(&Named.name(&1)) |> Iter.to_list())
end
`, `["post x", "page y"]
["post x", "page y"]
[Post{title: "x"}, Page{slug: "y"}]
post a post a Post{title: "a"} Post{title: "a"}
int 4 tag 4 int 4 tag 4
True
["int 1", "int 2"]
`)
}
