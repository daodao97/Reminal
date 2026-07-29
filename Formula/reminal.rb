class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.8"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.8/reminal_1.9.8_darwin_arm64.tar.gz"
      sha256 "a7bb1e0b6555640e96c1b1ecb6ea1eb69a711d34870478aed9dd6ae2760d3907"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.8/reminal_1.9.8_darwin_amd64.tar.gz"
      sha256 "402a85610a86dda0fca4bc4d29419891bd0a1f35bd503456c38a1d5843245dfa"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.8/reminal_1.9.8_linux_arm64.tar.gz"
      sha256 "aacb4b7948e9e3fae814c7d95e50a327d911872b73c7a9868067764593819a35"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.8/reminal_1.9.8_linux_amd64.tar.gz"
      sha256 "3a33af1af4b1932068443401f11df74668eb951a863fcb01083f1b0dd787a25c"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
    else
      bin.install "reminal"
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
