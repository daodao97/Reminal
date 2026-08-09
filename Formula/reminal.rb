class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.2"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.2/reminal_2.1.2_darwin_arm64.tar.gz"
      sha256 "ece65f998bd5273d2a16b96ba8243768dcadecce1fa88738e70a35e8c15afcba"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.2/reminal_2.1.2_darwin_amd64.tar.gz"
      sha256 "186a9233574cc57471e9376fa6464c099fb468f916a737adb36808f67f71a0de"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.2/reminal_2.1.2_linux_arm64.tar.gz"
      sha256 "40d9fcd09446c2abec8163662e7e96f4a6a9d733a5d282c00c67d9f77bb2c26d"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.2/reminal_2.1.2_linux_amd64.tar.gz"
      sha256 "fd4387fce2cc73028328874aab357b37c60f824a95ed4902495cc7f62a46d7bd"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
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
