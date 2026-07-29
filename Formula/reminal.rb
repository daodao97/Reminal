class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.6"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.6/reminal_1.9.6_darwin_arm64.tar.gz"
      sha256 "f3c93c774601fafb3e8d94e3f20b25577974bd99c4400d1f60bc6e55ef7a73c7"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.6/reminal_1.9.6_darwin_amd64.tar.gz"
      sha256 "029c0de943b8be020a3466dca11039350d5edf0b10a03696ba48af41f5a38fee"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.6/reminal_1.9.6_linux_arm64.tar.gz"
      sha256 "59fb2be1dcc51268ef907af9b09926a597884e7d5f56d642ca243f2c38cbc2e1"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.6/reminal_1.9.6_linux_amd64.tar.gz"
      sha256 "7ee4632139ab4db7d3f03ec016a06f93efe36e0d226d018e2af7ccf6d4a2a598"
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
