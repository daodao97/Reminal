class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.7"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.7/reminal_1.9.7_darwin_arm64.tar.gz"
      sha256 "2da662507f371790233bf35a1a5a40c0b65c6d1015b2441427bae6d04153d03b"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.7/reminal_1.9.7_darwin_amd64.tar.gz"
      sha256 "f1fb77bc43bfd78ee677ff1a168191d23095c7261ec0b9378af7a8c55e4f2ba6"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.7/reminal_1.9.7_linux_arm64.tar.gz"
      sha256 "b653eb69ee2fc958278dfbbffddcc93f7600f1879f58309cc4060d8f50263b5d"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.7/reminal_1.9.7_linux_amd64.tar.gz"
      sha256 "a37e1ea98cd9d14465f7e28cda38daf731e6a80ea9a86cf0af037f57ceb75f98"
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
