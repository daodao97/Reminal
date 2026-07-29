class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.10.2"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.2/reminal_1.10.2_darwin_arm64.tar.gz"
      sha256 "762a8d06e3513dc4b04c779170ef356a774b04e4a893f79e68907b40d16aa7ff"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.2/reminal_1.10.2_darwin_amd64.tar.gz"
      sha256 "3cc4f9de7bb5cef9b85f2e1780e0b375412239509871961416e1658413b60851"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.2/reminal_1.10.2_linux_arm64.tar.gz"
      sha256 "4dfcef29b8b5888652dd695e2883e9c285b03968d79ff088c16dbbaa14f8b30e"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.2/reminal_1.10.2_linux_amd64.tar.gz"
      sha256 "45def4200939fbf38a99cc7eea24ab0621b0be74565eb56d20a13a9701f10e8c"
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
