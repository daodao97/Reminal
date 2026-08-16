class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "3.0.2"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.2/reminal_3.0.2_darwin_arm64.tar.gz"
      sha256 "03ed487c60eed25230dfea6e4dad735ebf308630b0d8054349a27fc1beabf405"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.2/reminal_3.0.2_darwin_amd64.tar.gz"
      sha256 "14de8f72189d9c32d83583dfe70785b14cce5f8d01c2e3d88692c3d9bef9d0e4"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.2/reminal_3.0.2_linux_arm64.tar.gz"
      sha256 "17e10adee4e881a8ededd78f120e228ff22958b267252c21a02b166eae71d284"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.2/reminal_3.0.2_linux_amd64.tar.gz"
      sha256 "72aa739aa081a2ef0b44a2947791cc3a1fdd003de8bd5aa3a1db81cda6f5f558"
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
