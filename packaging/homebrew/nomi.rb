# The Homebrew formula for nomi's prebuilt release binaries.
#
# This file is the template. scripts/homebrew-formula.sh fills in the
# version and each archive's SHA-256 from a release's checksums file, and
# scripts/release.sh runs it. The tap repository, nomi-language/homebrew-nomi,
# holds the rendered copy as Formula/nomi.rb (docs/releasing.md).
class Nomi < Formula
  desc "Statically typed, immutable, functional-first programming language"
  homepage "https://nomi-lang.org"
  version "@VERSION@"
  license any_of: ["MIT", "Apache-2.0"]

  on_macos do
    on_arm do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_darwin_arm64.tar.gz"
      sha256 "@SHA256_DARWIN_ARM64@"
    end
    on_intel do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_darwin_amd64.tar.gz"
      sha256 "@SHA256_DARWIN_AMD64@"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_linux_arm64.tar.gz"
      sha256 "@SHA256_LINUX_ARM64@"
    end
    on_intel do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_linux_amd64.tar.gz"
      sha256 "@SHA256_LINUX_AMD64@"
    end
  end

  def install
    bin.install "nomi"
  end

  test do
    assert_match "nomi v#{version} ", shell_output("#{bin}/nomi --version")
    (testpath/"hello.nomi").write <<~NOMI
      import std::io

      fn main()
          io.print("Hello, Nomi!")
      end
    NOMI
    assert_equal "Hello, Nomi!\n", shell_output("#{bin}/nomi run #{testpath}/hello.nomi")
  end
end
