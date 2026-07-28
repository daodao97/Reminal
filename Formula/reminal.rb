class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.2"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.2/reminal_1.9.2_darwin_arm64.tar.gz"
      sha256 "26bab6be2713f8af2435c5d6b55ebc07674e98fea661609350eab871114b7554"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.2/reminal_1.9.2_darwin_amd64.tar.gz"
      sha256 "c462598d5c3773662ce22fe98765f3850641e4b34a66f5830cf0fd4bb6724ff0"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.2/reminal_1.9.2_linux_arm64.tar.gz"
      sha256 "9936d085e2363b33a74954de9d23aba2ae5819e6cd5c73119b823158986823a6"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.2/reminal_1.9.2_linux_amd64.tar.gz"
      sha256 "af09946d099543f8dfed907bbb2f918f2d24e2f8242eb6bf8c9d9bce888c3e57"
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
