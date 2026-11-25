# Rendered by scripts/homebrew-formula.sh in github.com/nomi-language/nomi
# from packaging/homebrew/nomi.rb and the checksums of release v0.0.1.
# Do not edit: render it again for the next release.
class Nomi < Formula
  desc "Statically typed, immutable, functional-first programming language"
  homepage "https://nomi-lang.org"
  version "0.0.1"
  license any_of: ["MIT", "Apache-2.0"]

  on_macos do
    on_arm do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_darwin_arm64.tar.gz"
      sha256 "713c5f23c85eb72b3733442ea760ab69da078b478998694744f182f65de76fbc"
    end
    on_intel do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_darwin_amd64.tar.gz"
      sha256 "7a97baa36b8a863a8a791a1ec461028890ec8806f074b7b01f1615a18eac31f3"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_linux_arm64.tar.gz"
      sha256 "68bd1aad314225222436b1c9e571197edf78f0899b550dfe091ba3b725d1ca45"
    end
    on_intel do
      url "https://github.com/nomi-language/nomi/releases/download/v#{version}/nomi_v#{version}_linux_amd64.tar.gz"
      sha256 "2e4fafb9d4e6e220c1776f623087ed1a1fd98762100e1a1541fb8d4a4d15befe"
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
